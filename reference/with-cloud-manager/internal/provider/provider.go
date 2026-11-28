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
	BareMetal         Kind = "bare-metal"
	Volume            Kind = "volume"
	Snapshot          Kind = "snapshot"
	Network           Kind = "network"
	Subnet            Kind = "subnet"
	Image             Kind = "image"
	SecurityGroup     Kind = "security-group"
	SecurityGroupRule Kind = "security-group-rule"
	ElasticIP         Kind = "elastic-ip"
	LoadBalancer      Kind = "load-balancer"
	DNSZone           Kind = "dns-zone"
	DatabaseInstance  Kind = "database-instance"
	CacheInstance     Kind = "cache-instance"
	KubernetesCluster Kind = "kubernetes-cluster"
	ObjectBucket      Kind = "object-bucket"
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
	ID              string
	Name            string
	Status          string
	InstanceType    string               `json:"instance_type,omitempty"`
	Links           []Link               `json:"links,omitempty"`
	Tags            map[string]string    `json:"tags"`
	PublicAddresses []string             `json:"public_addresses,omitempty"`
	LoadBalancer    *LoadBalancerDetails `json:"load_balancer,omitempty"`
	Bucket          *BucketDetails       `json:"object_bucket,omitempty"`
	Database        *DatabaseDetails     `json:"database_instance,omitempty"`
	Cache           *CacheDetails        `json:"cache_instance,omitempty"`
	Cluster         *ClusterDetails      `json:"kubernetes_cluster,omitempty"`
	Snapshot        *SnapshotDetails     `json:"snapshot,omitempty"`
	Volume          *VolumeDetails       `json:"volume,omitempty"`
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
		if len(object.Tags) > 256 || (object.InstanceType != "" && !ValidInstanceType(object.InstanceType)) {
			return &Failure{Kind: InvalidResponse}
		}
		if object.Bucket != nil && !object.ValidBucket() {
			return &Failure{Kind: InvalidResponse}
		}
		if object.Database != nil && !object.ValidDatabase() {
			return &Failure{Kind: InvalidResponse}
		}
		if object.Cache != nil && !object.ValidCache() {
			return &Failure{Kind: InvalidResponse}
		}
		if object.Cluster != nil && !object.ValidCluster() {
			return &Failure{Kind: InvalidResponse}
		}
		if object.Volume != nil && !object.Volume.Valid() {
			return &Failure{Kind: InvalidResponse}
		}
		if object.LoadBalancer != nil {
			if !object.ValidLoadBalancer() {
				return &Failure{Kind: InvalidResponse}
			}
		} else if !validPublicAddresses(object.PublicAddresses) {
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
	Unsupported        FailureKind = "unsupported"
	Unauthorized       FailureKind = "unauthorized"
	Throttled          FailureKind = "throttled"
	Transient          FailureKind = "transient"
	ExpiredCursor      FailureKind = "expired_cursor"
	InvalidResponse    FailureKind = "invalid_response"
	PreconditionFailed FailureKind = "precondition_failed"
	Absent             FailureKind = "absent"
	OutcomeUnknown     FailureKind = "outcome_unknown"
	DeletionBlocked    FailureKind = "deletion_blocked"
	DeletionDisabled   FailureKind = "deletion_disabled"
	CreationBlocked    FailureKind = "creation_blocked"
	VolumeBlocked      FailureKind = "volume_change_blocked"
	PowerDisabled      FailureKind = "instance_power_disabled"
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
	case Instance, BareMetal, Volume, Snapshot, Network, Subnet, Image, SecurityGroup, SecurityGroupRule, ElasticIP, LoadBalancer, DNSZone, DatabaseInstance, CacheInstance, KubernetesCluster, ObjectBucket, Region, Zone:
		return true
	default:
		return false
	}
}

type InstanceOperator interface {
	Observer
	ApplyInstance(context.Context, Scope, string, string) (string, error)
}

type InstancePowerPreparer interface {
	PrepareInstancePower(context.Context, Scope, string, string) (func(context.Context) (string, error), error)
}

type InstanceDeletionPreparer interface {
	PrepareInstanceDeletion(context.Context, Scope, string) (func(context.Context) (string, error), error)
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

type InstanceCreationPreparer interface {
	PrepareInstanceCreation(context.Context, Scope, InstanceSpec, string) (func(context.Context) (LaunchReceipt, error), error)
}

type InstanceCreationReceiptObserver interface {
	FindCreatedInstanceWithReceipt(context.Context, Scope, InstanceSpec, string, LaunchReceipt) (Object, error)
}

type LaunchReceipt struct {
	RequestID  string
	ResourceID string
}

type NetworkSpec struct {
	Name          string        `json:"name"`
	CIDR          string        `json:"cidr,omitempty"`
	ParentID      string        `json:"parent_id,omitempty"`
	Rule          *SecurityRule `json:"rule,omitempty"`
	Zone          string        `json:"zone,omitempty"`
	BandwidthMbps int           `json:"bandwidth_mbps,omitempty"`
	AddressPool   string        `json:"address_pool,omitempty"`
}

type NetworkCreator interface {
	CreateNetwork(ctx context.Context, scope Scope, spec NetworkSpec, token string) (LaunchReceipt, error)
	FindCreatedNetwork(ctx context.Context, scope Scope, spec NetworkSpec, token, resourceID string) (Object, error)
}

type NetworkCreationPreparer interface {
	PrepareNetworkCreation(context.Context, Scope, NetworkSpec, string) (func(context.Context) (LaunchReceipt, error), error)
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

type VolumePreparer interface {
	PrepareVolume(context.Context, Scope, VolumeChange) (func(context.Context) (string, error), error)
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
