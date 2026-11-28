package provider

import (
	"context"
	"regexp"
)

var instanceTypePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func ValidInstanceType(size string) bool {
	return instanceTypePattern.MatchString(size)
}

type InstanceResize struct {
	ID              string
	CurrentType     string
	TargetType      string
	AcceptAutoStart bool
}

func (r InstanceResize) Valid() bool {
	return r.ID != "" && r.CurrentType != r.TargetType &&
		ValidInstanceType(r.CurrentType) && ValidInstanceType(r.TargetType)
}

func (r InstanceResize) ValidFor(name string) bool {
	switch name {
	case TencentCVM, AlibabaECS, HuaweiECS:
		return r.Valid() && r.AcceptAutoStart == (name == TencentCVM)
	default:
		return false
	}
}

func InstanceStoppedState(name string) string {
	switch name {
	case TencentCVM:
		return "STOPPED"
	case AlibabaECS:
		return "Stopped"
	case HuaweiECS:
		return "SHUTOFF"
	default:
		return ""
	}
}

type InstanceResizer interface {
	PrepareInstanceResize(context.Context, Scope, InstanceResize) (func(context.Context) (string, error), error)
	ObserveInstanceResize(context.Context, Scope, InstanceResize, string) (ActionResult, string, error)
}

type InstanceResizeOptions struct {
	Zone  string
	Types []string
}
