package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/birdordev/plumego/x/messaging/mq"
)

const (
	recordTaskType     = "demo.record"
	computeTaskType    = "demo.compute"
	ioTaskType         = "demo.io"
	exportTaskType     = "demo.export"
	checkpointTaskType = "demo.checkpoint"
	reserveTaskType    = "demo.reserve"
	releaseTaskType    = "demo.release"
	sandboxTaskType    = "sandbox.shell"
)

type taskOptions struct {
	TaskVersion  int               `json:"task_version,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	ResourceRefs []string          `json:"resource_refs,omitempty"`
	Overlap      *overlapOptions   `json:"overlap,omitempty"`
	Priority     string            `json:"priority,omitempty"`
}

type taskContract struct {
	taskType string
	version  int
	topic    string
}

func registeredTaskContracts() [10]taskContract {
	return [...]taskContract{
		{recordTaskType, 1, recordTaskType},
		{recordTaskType, 2, "demo.record.v2"},
		{computeTaskType, 1, computeTaskType},
		{ioTaskType, 1, ioTaskType},
		{exportTaskType, 1, exportTaskType},
		{checkpointTaskType, 1, checkpointTaskType},
		{reserveTaskType, 1, reserveTaskType},
		{releaseTaskType, 1, releaseTaskType},
		{sandboxTaskType, 1, sandboxTaskType},
		{inventoryTaskType, 1, inventoryTaskType},
	}
}

func lookupTaskContract(taskType string, version int) (taskContract, bool) {
	if version == 0 {
		version = 1
	}
	for _, c := range registeredTaskContracts() {
		if c.taskType == taskType && c.version == version {
			return c, true
		}
	}
	return taskContract{}, false
}

func taskContractForTopic(topic string) (taskContract, bool) {
	for _, c := range registeredTaskContracts() {
		if c.topic == topic {
			return c, true
		}
	}
	return taskContract{}, false
}

func validateTaskContract(topic string, payload json.RawMessage, options taskOptions) (taskContract, []byte, error) {
	registered, ok := lookupTaskContract(topic, options.TaskVersion)
	_, validPriority := taskPriority(options.Priority)
	if !ok || len(payload) > 64<<10 || !json.Valid(payload) ||
		len(options.Metadata) > 8 || len(options.ResourceRefs) > 8 || !options.Overlap.valid() || !validPriority {
		return taskContract{}, nil, errInvalidTask
	}
	for key, val := range options.Metadata {
		if len(key) > 64 || !runCursorID.MatchString(key) || len(val) > 256 ||
			!utf8.ValidString(val) || strings.ContainsRune(val, 0) {
			return taskContract{}, nil, errInvalidTask
		}
	}
	for _, ref := range options.ResourceRefs {
		if !runCursorID.MatchString(ref) {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.version == 2 {
		var input struct {
			Message string `json:"message"`
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Message) == "" || len(input.Message) > 4096 {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.taskType == ioTaskType {
		if _, err := ioDelay(payload); err != nil {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.taskType == exportTaskType {
		if _, err := exportSize(payload); err != nil {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.taskType == checkpointTaskType {
		if _, err := parseCheckpointInput(payload); err != nil {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.taskType == reserveTaskType {
		if _, err := reservationQuantity(payload); err != nil {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.taskType == sandboxTaskType {
		if _, err := parseSandboxScript(payload); err != nil || len(options.ResourceRefs) != 0 {
			return taskContract{}, nil, errInvalidTask
		}
	}
	if registered.taskType == inventoryTaskType {
		if _, err := parseInventoryCommand(payload); err != nil {
			return taskContract{}, nil, errInvalidTask
		}
	}
	encoded, err := json.Marshal(options)
	if err != nil || len(encoded) > 4096 {
		return taskContract{}, nil, errInvalidTask
	}
	return registered, encoded, nil
}

func taskPriority(value string) (mq.MessagePriority, bool) {
	switch value {
	case "", "normal":
		return mq.PriorityNormal, true
	case "low":
		return mq.PriorityLow, true
	case "high":
		return mq.PriorityHigh, true
	default:
		return mq.PriorityNormal, false
	}
}

func workerTaskContracts(versions string) ([]taskContract, error) {
	if versions == "" {
		versions = "1,2"
	}
	parts := strings.Split(versions, ",")
	contracts := make([]taskContract, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		version, err := strconv.Atoi(strings.TrimSpace(part))
		c, ok := lookupTaskContract(recordTaskType, version)
		if err != nil || !ok || version == 0 || seen[version] {
			return nil, errInvalidTask
		}
		seen[version] = true
		contracts = append(contracts, c)
	}
	return contracts, nil
}

func (c taskContract) handle(record mq.TaskHandler) mq.TaskHandler {
	return func(ctx context.Context, task mq.Task) error {
		version := strconv.Itoa(c.version)
		storedType, storedVersion := task.Meta["task_type"], task.Meta["task_version"]
		if task.Topic != c.topic ||
			(storedType != c.taskType && !(c.version == 1 && storedType == "")) ||
			(storedVersion != version && !(c.version == 1 && storedVersion == "")) {
			return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		var options taskOptions
		if raw := task.Meta["task_options"]; raw != "" {
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.DisallowUnknownFields()
			if len(raw) > 4096 || !strings.HasPrefix(strings.TrimSpace(raw), "{") ||
				!json.Valid([]byte(raw)) || decoder.Decode(&options) != nil {
				return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
			}
		}
		actual, _, err := validateTaskContract(c.taskType, task.Payload, options)
		if err != nil || actual != c {
			return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		return record(ctx, task)
	}
}
