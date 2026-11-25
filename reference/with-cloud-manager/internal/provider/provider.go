package provider

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Kind string

const (
	Instance          Kind = "instance"
	Volume            Kind = "volume"
	Network           Kind = "network"
	Subnet            Kind = "subnet"
	Image             Kind = "image"
	SecurityGroup     Kind = "security-group"
	SecurityGroupRule Kind = "security-group-rule"
	Region            Kind = "region"
	Zone              Kind = "zone"
)

type Scope struct {
	Provider       string
	RemoteIdentity string
	Region         string
	Kind           Kind
}

type Object struct {
	ID     string
	Name   string
	Status string
	Links  []Link            `json:"links,omitempty"`
	Tags   map[string]string `json:"tags"`
}

type Link struct {
	Kind     Kind   `json:"kind"`
	ID       string `json:"id"`
	Relation string `json:"relation"`
}

func (o *Object) Link(kind Kind, id, relation string) {
	if id != "" {
		o.Links = append(o.Links, Link{Kind: kind, ID: id, Relation: relation})
	}
}

type Page struct {
	Objects []Object
	Next    string
}

const (
	MaxPageObjects  = 1000
	MaxCursorBytes  = 8192
	MaxObjectIDSize = 512
)

func (p Page) Validate(cursor string) error {
	if len(p.Objects) > MaxPageObjects || len(p.Next) > MaxCursorBytes || (p.Next != "" && p.Next == cursor) {
		return &Failure{Kind: InvalidResponse}
	}
	for _, object := range p.Objects {
		if object.ID == "" || len(object.ID) > MaxObjectIDSize || len(object.Name) > 1024 || len(object.Status) > 256 || len(object.Links) > 256 {
			return &Failure{Kind: InvalidResponse}
		}
		if len(object.Tags) > 256 {
			return &Failure{Kind: InvalidResponse}
		}
		for key, value := range object.Tags {
			if key == "" || len(key) > 128 || len(value) > 256 || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
				return &Failure{Kind: InvalidResponse}
			}
		}

		for _, link := range object.Links {
			if !link.Kind.Valid() || link.ID == "" || len(link.ID) > MaxObjectIDSize || link.Relation == "" || len(link.Relation) > 32 {
				return &Failure{Kind: InvalidResponse}
			}
		}
	}
	return nil
}

type Discovery interface {
	Discover(context.Context, Scope, string) (Page, error)
}

type Observer interface {
	Observe(context.Context, Scope, string) (Object, error)
}

type FailureKind string

const (
	Unsupported      FailureKind = "unsupported"
	Unauthorized     FailureKind = "unauthorized"
	Throttled        FailureKind = "throttled"
	Transient        FailureKind = "transient"
	ExpiredCursor    FailureKind = "expired_cursor"
	InvalidResponse  FailureKind = "invalid_response"
	Absent           FailureKind = "absent"
	OutcomeUnknown   FailureKind = "outcome_unknown"
	DeletionBlocked  FailureKind = "deletion_blocked"
	DeletionDisabled FailureKind = "deletion_disabled"
	CreationBlocked  FailureKind = "creation_blocked"
	VolumeBlocked    FailureKind = "volume_change_blocked"
)

type Failure struct {
	Kind       FailureKind
	RetryAfter time.Duration
}

func (f *Failure) Error() string {
	return "provider failure: " + string(f.Kind)
}

func IsFailure(err error, kind FailureKind) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Kind == kind
}

func (k Kind) Valid() bool {
	switch k {
	case Instance, Volume, Network, Subnet, Image, SecurityGroup, SecurityGroupRule, Region, Zone:
		return true
	default:
		return false
	}
}

type InstanceOperator interface {
	Observer
	ApplyInstance(context.Context, Scope, string, string) (string, error)
}

type InstanceSpec struct {
	Name          string `json:"name"`
	ImageID       string `json:"image_id"`
	Size          string `json:"size"`
	NetworkID     string `json:"network_id"`
	SecurityGroup string `json:"security_group_id"`
}

type InstanceCreator interface {
	CreateInstance(ctx context.Context, scope Scope, spec InstanceSpec, token string) (LaunchReceipt, error)
	FindCreatedInstance(ctx context.Context, scope Scope, spec InstanceSpec, token, instanceID string) (Object, error)
}

type LaunchReceipt struct {
	RequestID  string
	ResourceID string
}

type NetworkSpec struct {
	Name     string        `json:"name"`
	CIDR     string        `json:"cidr,omitempty"`
	ParentID string        `json:"parent_id,omitempty"`
	Rule     *SecurityRule `json:"rule,omitempty"`
}

type NetworkCreator interface {
	CreateNetwork(ctx context.Context, scope Scope, spec NetworkSpec, token string) (LaunchReceipt, error)
	FindCreatedNetwork(ctx context.Context, scope Scope, spec NetworkSpec, token, resourceID string) (Object, error)
}

type VolumeChange struct {
	InstanceID string
	VolumeID   string
	Device     string
	Attach     bool
}

type VolumeOperator interface {
	ApplyVolume(ctx context.Context, scope Scope, change VolumeChange) (requestID string, err error)
	ObserveVolume(ctx context.Context, scope Scope, change VolumeChange) (reached bool, err error)
}

type ActionResult string

const (
	ActionPending   ActionResult = "pending"
	ActionSucceeded ActionResult = "succeeded"
	ActionFailed    ActionResult = "failed"
)

type RebootObserver interface {
	ObserveReboot(ctx context.Context, scope Scope, instanceID, requestID string) (ActionResult, error)
}

type DeletionObserver interface {
	ObserveDeletion(ctx context.Context, scope Scope, instanceID, requestID string) (ActionResult, error)
}
