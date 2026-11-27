package provider

import "context"

const (
	StageBareMetalPXE  = "stage-bare-metal-pxe"
	CancelBareMetalPXE = "cancel-bare-metal-pxe"
)

type BareMetalBootOperator interface {
	PrepareBareMetalPXE(context.Context, Scope, string, string) (func(context.Context) (string, error), error)
	ObserveBareMetalPXE(context.Context, Scope, string, string) (bool, error)
}

type BareMetalDeploymentOperator interface {
	PrepareBareMetalDeploymentBoot(context.Context, Scope, string) (func(context.Context) (string, error), error)
	ObserveBareMetalDeploymentBoot(context.Context, Scope, string) (bool, error)
}
