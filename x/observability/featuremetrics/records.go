package featuremetrics

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/metrics"
)

const (
	labelOperation = "operation"
	labelTopic     = "topic"
	labelKVKey     = "key"
	labelPanicked  = "panicked"
	labelHit       = "hit"
	labelAddr      = "addr"
	labelTransport = "transport"
	labelBytes     = "bytes"
	labelDriver    = "driver"
	labelQuery     = "query"
	labelTable     = "table"
	labelRows      = "rows"
)

// ObservePubSub records a pubsub metric through a generic recorder.
func ObservePubSub(recorder metrics.Recorder, ctx context.Context, operation, topic string, duration time.Duration, err error) {
	if recorder == nil {
		return
	}
	recorder.Record(ctx, PubSubRecord(operation, topic, duration, err))
}

// PubSubRecord builds a pubsub metric record.
func PubSubRecord(operation, topic string, duration time.Duration, err error) metrics.MetricRecord {
	normalizedOperation := normalizeMetricOperation(operation)
	return metrics.MetricRecord{
		Name:  "pubsub_" + normalizedOperation,
		Value: duration.Seconds(),
		Labels: metrics.MetricLabels{
			labelOperation: normalizedOperation,
			labelTopic:     topic,
		},
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// ObserveMQ records an MQ metric through a generic recorder.
func ObserveMQ(recorder metrics.Recorder, ctx context.Context, operation, topic string, duration time.Duration, err error, panicked bool) {
	if recorder == nil {
		return
	}
	recorder.Record(ctx, MQRecord(operation, topic, duration, err, panicked))
}

// MQRecord builds an MQ metric record.
func MQRecord(operation, topic string, duration time.Duration, err error, panicked bool) metrics.MetricRecord {
	normalizedOperation := normalizeMetricOperation(operation)
	return metrics.MetricRecord{
		Name:  "mq_" + normalizedOperation,
		Value: duration.Seconds(),
		Labels: metrics.MetricLabels{
			labelOperation: normalizedOperation,
			labelTopic:     topic,
			labelPanicked:  boolLabel(panicked),
		},
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// ObserveKV records a KV metric through a generic recorder.
func ObserveKV(recorder metrics.Recorder, ctx context.Context, operation, key string, duration time.Duration, err error, hit bool) {
	if recorder == nil {
		return
	}
	recorder.Record(ctx, KVRecord(operation, key, duration, err, hit))
}

// KVRecord builds a KV metric record.
func KVRecord(operation, key string, duration time.Duration, err error, hit bool) metrics.MetricRecord {
	normalizedOperation := normalizeMetricOperation(operation)
	labels := metrics.MetricLabels{
		labelOperation: normalizedOperation,
		labelHit:       boolLabel(hit),
	}
	if key != "" {
		labels[labelKVKey] = key
	}
	return metrics.MetricRecord{
		Name:      "kv_" + normalizedOperation,
		Value:     duration.Seconds(),
		Labels:    labels,
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// ObserveIPC records an IPC metric through a generic recorder.
func ObserveIPC(recorder metrics.Recorder, ctx context.Context, operation, addr, transport string, bytes int, duration time.Duration, err error) {
	if recorder == nil {
		return
	}
	recorder.Record(ctx, IPCRecord(operation, addr, transport, bytes, duration, err))
}

// IPCRecord builds an IPC metric record.
func IPCRecord(operation, addr, transport string, bytes int, duration time.Duration, err error) metrics.MetricRecord {
	normalizedOperation := normalizeMetricOperation(operation)
	labels := metrics.MetricLabels{
		labelOperation: normalizedOperation,
		labelTransport: transport,
	}
	if addr != "" {
		labels[labelAddr] = addr
	}
	if bytes > 0 {
		labels[labelBytes] = strconv.Itoa(bytes)
	}
	return metrics.MetricRecord{
		Name:      "ipc_" + normalizedOperation,
		Value:     duration.Seconds(),
		Labels:    labels,
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// ObserveDB records a DB metric through a generic recorder.
func ObserveDB(recorder metrics.Recorder, ctx context.Context, operation, driver, query string, rows int, duration time.Duration, err error) {
	if recorder == nil {
		return
	}
	recorder.Record(ctx, DBRecord(operation, driver, query, rows, duration, err))
}

// DBRecord builds a DB metric record.
func DBRecord(operation, driver, query string, rows int, duration time.Duration, err error) metrics.MetricRecord {
	normalizedOperation := normalizeMetricOperation(operation)
	labels := metrics.MetricLabels{
		labelOperation: normalizedOperation,
	}
	if driver != "" {
		labels[labelDriver] = driver
	}
	if query != "" {
		const maxQueryLen = 100
		if len(query) > maxQueryLen {
			labels[labelQuery] = query[:maxQueryLen] + "..."
		} else {
			labels[labelQuery] = query
		}
	}
	if rows > 0 {
		labels[labelRows] = strconv.Itoa(rows)
	}
	if table := ExtractTableName(query); table != "" {
		labels[labelTable] = table
	}
	return metrics.MetricRecord{
		Name:      "db_" + normalizedOperation,
		Value:     duration.Seconds(),
		Labels:    labels,
		Duration:  duration,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// ExtractTableName attempts to extract the primary table name from a SQL query.
func ExtractTableName(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return ""
	}

	for i := 0; i < len(fields); i++ {
		upper := strings.ToUpper(fields[i])
		switch upper {
		case "FROM", "INTO":
			if i+1 < len(fields) {
				next := fields[i+1]
				if strings.HasPrefix(next, "(") {
					continue
				}
				return cleanTableName(next)
			}
		case "UPDATE":
			if i+1 < len(fields) {
				return cleanTableName(fields[i+1])
			}
		case "TABLE":
			next := i + 1
			if next < len(fields) && strings.EqualFold(fields[next], "IF") {
				next++
				if next < len(fields) && strings.EqualFold(fields[next], "NOT") {
					next++
				}
				if next < len(fields) && strings.EqualFold(fields[next], "EXISTS") {
					next++
				}
			}
			if next < len(fields) {
				return cleanTableName(fields[next])
			}
		}
	}

	return ""
}

func normalizeMetricOperation(operation string) string {
	normalized := strings.ToLower(strings.TrimSpace(operation))
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func cleanTableName(value string) string {
	value = strings.TrimRight(value, ",;()")
	if value == "" {
		return ""
	}

	value = unquoteIdent(value)
	if idx := strings.LastIndexByte(value, '.'); idx >= 0 && idx+1 < len(value) {
		value = unquoteIdent(value[idx+1:])
	}
	return value
}

func unquoteIdent(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') ||
			(first == '`' && last == '`') ||
			(first == '[' && last == ']') {
			value = value[1 : len(value)-1]
		}
	}
	return value
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
