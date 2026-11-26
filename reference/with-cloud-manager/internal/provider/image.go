package provider

import (
	"context"
	"regexp"
	"strings"
)

var (
	tencentImageID = regexp.MustCompile(`^img-[a-z0-9]{1,64}$`)
	alibabaImageID = regexp.MustCompile(`^m-[a-z0-9]{1,64}$`)
	huaweiImageID  = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
)

func ValidOwnedImage(name, id string) bool {
	switch name {
	case TencentCVM:
		return tencentImageID.MatchString(id)
	case AlibabaECS:
		return alibabaImageID.MatchString(id)
	case HuaweiECS:
		return huaweiImageID.MatchString(id)
	default:
		return false
	}
}

type ImageDeleter interface {
	PrepareImageDeletion(context.Context, Scope, string) (func(context.Context) (string, error), error)
	ObserveImageDeletion(context.Context, Scope, string) (ActionResult, error)
}

type ImageSpec struct {
	Name       string `json:"name"`
	InstanceID string `json:"instance_id"`
}

var imageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,59}$`)
var tencentImageSource = regexp.MustCompile(`^ins-[a-z0-9]{1,64}$`)
var alibabaImageSource = regexp.MustCompile(`^i-[a-z0-9]{1,64}$`)

func (s ImageSpec) ValidFor(name string) bool {
	if !imageName.MatchString(s.Name) {
		return false
	}
	switch name {
	case TencentCVM:
		return tencentImageSource.MatchString(s.InstanceID)
	case AlibabaECS:
		return alibabaImageSource.MatchString(s.InstanceID)
	case HuaweiECS:
		return huaweiImageID.MatchString(s.InstanceID)
	default:
		return false
	}
}

func (s ImageSpec) Source() Link {
	return Link{Kind: Instance, ID: s.InstanceID, Relation: "created_from"}
}

type ImageCreator interface {
	PrepareImageCreation(context.Context, Scope, ImageSpec, string) (func(context.Context) (LaunchReceipt, error), error)
	FindCreatedImage(context.Context, Scope, ImageSpec, string, LaunchReceipt) (Object, error)
}

type ImageCopySpec struct {
	ImageID string `json:"asset_id"`
	Name    string `json:"name"`
}

func (s ImageCopySpec) ValidFor(name string) bool {
	return imageName.MatchString(s.Name) && ValidOwnedImage(name, s.ImageID) &&
		(name != AlibabaECS || !strings.HasPrefix(strings.ToLower(s.Name), "aliyun"))
}

type ImageCopier interface {
	PrepareImageCopy(context.Context, Scope, Scope, ImageCopier, ImageCopySpec, string) (func(context.Context) (LaunchReceipt, error), error)
	FindCopiedImage(context.Context, Scope, Scope, ImageCopier, ImageCopySpec, string, LaunchReceipt) (Object, error)
}
