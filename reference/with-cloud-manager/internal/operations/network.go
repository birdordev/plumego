package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0020_network_operations.sql
var networkSchema string

//go:embed 0021_security_group_creation.sql
var securityGroupSchema string

//go:embed 0031_security_group_rules.sql
var securityRuleSchema string

//go:embed 0033_subnet_zone.sql
var subnetZoneSchema string

//go:embed 0037_elastic_ip_creation.sql
var elasticIPCreationSchema string

const createElasticIP = "create-elastic-ip"

type NetworkInput struct {
	Account string        `json:"account"`
	Region  string        `json:"region"`
	Kind    provider.Kind `json:"kind"`
	provider.NetworkSpec
}

type networkIntent struct {
	provider.NetworkSpec
	token, resourceID           string
	parentScope, parentRevision int64
	observer                    creationObserver
	snapshotAllowAttached       bool
}

func migrateNetwork(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0020_network_operations", networkSchema)
}

func isNetworkCreation(action string) bool {
	return action == "create-network" || action == "create-subnet" || action == "create-security-group" ||
		action == "create-security-group-rule" || action == createElasticIP
}

func isResourceCreation(action string) bool {
	return isNetworkCreation(action) || action == createSnapshot || action == restoreSnapshot || action == createImage || action == copyImage
}

func validCreationInput(input NetworkInput, parentRevision int64) bool {
	if !operationKey.MatchString(input.Account) || !operationKey.MatchString(input.Region) {
		return false
	}
	if input.Zone != "" && input.Kind != provider.Subnet {
		return false
	}
	if input.Kind != provider.ElasticIP && (input.BandwidthMbps != 0 || input.AddressPool != "") {
		return false
	}
	if input.Kind == provider.SecurityGroupRule {
		return operationKey.MatchString(input.ParentID) && parentRevision > 0 && input.Name == "" && input.CIDR == "" && input.Rule != nil && input.Rule.Valid()
	}
	if input.Rule != nil || !instanceName.MatchString(input.Name) {
		return false
	}
	if input.CIDR != "" {
		prefix, err := netip.ParsePrefix(input.CIDR)
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Masked().String() != input.CIDR {
			return false
		}
	}
	switch input.Kind {
	case provider.Snapshot, provider.Image:
		return operationKey.MatchString(input.ParentID) && parentRevision > 0 && input.CIDR == ""
	case provider.ElasticIP:
		return parentRevision == 0
	case provider.Network:
		return input.ParentID == "" && parentRevision == 0
	case provider.Subnet:
		return operationKey.MatchString(input.ParentID) && parentRevision > 0 && input.CIDR != ""
	case provider.SecurityGroup:
		return input.CIDR == "" && !strings.EqualFold(input.Name, "default") &&
			((input.ParentID == "" && parentRevision == 0) || (operationKey.MatchString(input.ParentID) && parentRevision > 0))
	}
	return false
}

func (s Store) SubmitNetworkCreation(ctx context.Context, token string, scope identity.Scope, key string, parentRevision int64, input NetworkInput) (Operation, error) {
	if input.Kind == provider.Snapshot || input.Kind == provider.Image {
		return Operation{}, ErrInvalid
	}
	return s.submitResourceCreation(ctx, token, scope, key, parentRevision, input, false)
}

func (s Store) submitResourceCreation(ctx context.Context, token string, scope identity.Scope, key string, parentRevision int64, input NetworkInput, allowAttached bool) (Operation, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !operationKey.MatchString(key) || !validCreationInput(input, parentRevision) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		NetworkInput
		ParentRevision int64
		AllowAttached  bool `json:"snapshot_allow_attached,omitempty"`
	}{input, parentRevision, allowAttached})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte("network-create\x00"), data...))
	rule, err := json.Marshal(input.Rule)
	if err != nil {
		return Operation{}, err
	}
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		access, err := authorizeCreation(ctx, tx, actor, input.Account)
		if err != nil {
			return err
		}
		if !validNetworkVendor(input, access.Provider) {
			return ErrInvalid
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		found, err := findReplay(ctx, tx, actor, key, digest, &op)
		if err != nil || found {
			return err
		}
		if err := checkBudget(ctx, tx, actor); err != nil {
			return err
		}
		parent, err := validateNetworkParent(ctx, tx, actor, input, parentRevision, "")
		if err != nil {
			return err
		}
		action := "create-" + string(input.Kind)
		status, err := initialStatus(ctx, tx, actor, action)
		if err != nil {
			return err
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,account_id,region,action,status,resource_kind,
			idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0) RETURNING `+operationFields,
			id, actor.Tenant, actor.Project, actor.Principal, input.Account, input.Region, action, status, input.Kind, key, digest[:],
			access.CredentialRevision, access.MembershipRevision), &op)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents
			(operation_id,launch_token,name,cidr,parent_scope_id,parent_id,parent_revision,rule_spec,zone,bandwidth_mbps,address_pool,snapshot_allow_attached)
			VALUES ($1,$2,$3,NULLIF($4,'')::cidr,NULLIF($5::bigint,0),NULLIF($6,''),$7,NULLIF($8::jsonb,'null'::jsonb),$9,$10,$11,$12)`,
			id, rand.Text(), input.Name, input.CIDR, parent, input.ParentID, parentRevision, string(rule), input.Zone,
			input.BandwidthMbps, input.AddressPool, allowAttached); err != nil {
			return err
		}
		if input.Kind == provider.Image {
			if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_image_reservations
				(scope_id,instance_id,operation_id) VALUES ($1,$2,$3)`, parent, input.ParentID, op.ID); err != nil {
				return err
			}
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func validNetworkVendor(input NetworkInput, name string) bool {
	if input.Kind == provider.Image {
		return (provider.ImageSpec{Name: input.Name, InstanceID: input.ParentID}).ValidFor(name)
	}
	if input.Kind == provider.Snapshot {
		return (provider.SnapshotSpec{Name: input.Name, VolumeID: input.ParentID}).ValidFor(name)
	}
	if input.Kind == provider.ElasticIP {
		return input.ValidPublicCloudElasticIP(name)
	}
	if publicCloudProvider(name) {
		return (input.Kind == provider.Network && input.ValidPublicCloudVPC(name)) ||
			(input.Kind == provider.Subnet && input.ValidPublicCloudSubnet(name, input.Region)) ||
			(input.Kind == provider.SecurityGroup && input.ValidPublicCloudSecurityGroup(name)) ||
			input.Kind == provider.SecurityGroupRule
	}
	if input.Zone != "" {
		return false
	}
	if input.Kind == provider.SecurityGroupRule {
		return name == "aws-ec2" || name == "openstack"
	}
	if input.Kind == provider.SecurityGroup {
		switch name {
		case "aws-ec2":
			return input.ParentID != "" && !strings.HasPrefix(strings.ToLower(input.Name), "sg-")
		case "openstack":
			return input.ParentID == ""
		}
		return false
	}
	if name == "aws-ec2" {
		prefix, err := netip.ParsePrefix(input.CIDR)
		return err == nil && prefix.Bits() >= 16 && prefix.Bits() <= 28
	}
	return name == "openstack" && (input.Kind != provider.Network || input.CIDR == "")
}

func networkParent(kind provider.Kind, id string) provider.Link {
	if kind == provider.Image {
		return (provider.ImageSpec{InstanceID: id}).Source()
	}
	if kind == provider.Snapshot {
		return provider.Link{Kind: provider.Volume, ID: id, Relation: "snapshot_of"}
	}
	if kind == provider.SecurityGroupRule {
		return provider.Link{Kind: provider.SecurityGroup, ID: id, Relation: "in_security_group"}
	}
	return provider.Link{Kind: provider.Network, ID: id, Relation: "in_network"}
}

func validateNetworkParent(ctx context.Context, tx *sql.Tx, actor identity.Scope, input NetworkInput, revision int64, opID string) (int64, error) {
	if input.ParentID == "" {
		return 0, nil
	}
	parentKind := networkParent(input.Kind, input.ParentID).Kind
	parent, err := inventory.ResolveNetworkMutation(ctx, tx, actor, input.Account, input.Region, input.ParentID, parentKind)
	if err != nil {
		return 0, err
	}
	if parent.OwnershipRevision != revision {
		return 0, identity.ErrAccountStale
	}
	if parentKind == provider.Network || parentKind == provider.SecurityGroup {
		active, err := inventory.NetworkDeletionActive(ctx, tx, parent.ScopeID, input.ParentID)
		if err != nil {
			return 0, err
		}
		if active {
			return 0, ErrConflict
		}
	}
	const releasedIntent = `op.status='succeeded' AND EXISTS (
		SELECT 1 FROM cmp_inventory_objects AS o
		WHERE (o.scope_id,o.external_id)=(op.scope_id,op.external_id) AND o.deletion_confirmed_at IS NOT NULL)`
	const conflictQuery = `SELECT EXISTS (SELECT 1 FROM cmp_network_intents AS intent
		JOIN cmp_instance_operations AS op USING (operation_id)
		WHERE intent.parent_scope_id=$1 AND intent.parent_id=$2 AND op.operation_id<>$3 AND op.status<>'failed' AND `
	var conflict bool
	switch input.Kind {
	case provider.Image:
		conflict, err = inventory.InstanceImageCreationActive(ctx, tx, parent.ScopeID, input.ParentID, opID)
		if err == nil && !conflict {
			err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_instance_operations
				WHERE (scope_id,external_id)=($1,$2) AND status IN `+activeOperationStatuses+`)`,
				parent.ScopeID, input.ParentID).Scan(&conflict)
		}
	case provider.Snapshot:
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM cmp_instance_operations
			WHERE (volume_scope_id,volume_id)=($1,$2) AND status IN `+activeOperationStatuses+`
		) OR `+snapshotWriteConflict+" OR "+retirementDiskReserved, parent.ScopeID, input.ParentID, opID).Scan(&conflict)
	case provider.Subnet:
		err = tx.QueryRowContext(ctx, conflictQuery+`intent.cidr && $4::cidr AND NOT (`+releasedIntent+`))`, parent.ScopeID, input.ParentID, opID, input.CIDR).Scan(&conflict)
	case provider.SecurityGroupRule:
		rule, marshalErr := json.Marshal(input.Rule)
		if marshalErr != nil {
			return 0, marshalErr
		}
		err = tx.QueryRowContext(ctx, conflictQuery+`intent.rule_spec=$4::jsonb AND NOT (`+releasedIntent+`))`,
			parent.ScopeID, input.ParentID, opID, string(rule)).Scan(&conflict)
	}
	if err == nil && conflict {
		return 0, ErrConflict
	}
	return parent.ScopeID, err
}

func checkNetworkIntent(ctx context.Context, tx *sql.Tx, w *work, beforeWrite bool) error {
	network := &w.network
	var rule []byte
	err := tx.QueryRowContext(ctx, `SELECT launch_token,provider_resource_id,name,COALESCE(cidr::text,''),
		COALESCE(parent_scope_id,0),COALESCE(parent_id,''),parent_revision,
		observation_reviewer,observation_membership_revision,observation_credential_revision,rule_spec,zone,bandwidth_mbps,address_pool,snapshot_allow_attached
		FROM cmp_network_intents WHERE operation_id=$1`, w.ID).Scan(&network.token, &network.resourceID, &network.Name,
		&network.CIDR, &network.parentScope, &network.ParentID, &network.parentRevision,
		&network.observer.principal, &network.observer.membership, &network.observer.credential, &rule, &network.Zone,
		&network.BandwidthMbps, &network.AddressPool, &network.snapshotAllowAttached)
	if err != nil {
		return err
	}
	if len(rule) != 0 {
		if err := json.Unmarshal(rule, &network.Rule); err != nil {
			return err
		}
	}
	if w.Action == createImage {
		var reserved bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_instance_image_reservations
			WHERE (scope_id,instance_id,operation_id)=($1,$2,$3))`, network.parentScope, network.ParentID, w.ID).Scan(&reserved)
		if err != nil {
			return err
		}
		if !reserved {
			return identity.ErrAccountStale
		}
	}
	access, err := checkCreationAuthority(ctx, tx, w, network.observer, beforeWrite)
	if err != nil {
		return err
	}
	mandatory := w.ResourceKind == provider.SecurityGroupRule || w.ResourceKind == provider.ElasticIP || w.ResourceKind == provider.Snapshot || w.ResourceKind == provider.Image
	if w.Status == "queued" && (mandatory || publicCloudProvider(access.Provider)) {
		if err := checkDispatchApproval(ctx, tx, w, mandatory); err != nil {
			return err
		}
	}
	if !beforeWrite {
		if network.ParentID == "" {
			return nil
		}
		actor := w.actor
		if network.observer.principal != "" {
			actor.Principal = network.observer.principal
		}
		parent, err := inventory.ResolveNetworkObservation(ctx, tx, actor, w.Account, w.Region, network.ParentID, networkParent(w.ResourceKind, network.ParentID).Kind)
		if err == nil && (parent.ScopeID != network.parentScope || parent.OwnershipRevision != network.parentRevision) {
			return identity.ErrAccountStale
		}
		return err
	}
	parent, err := validateNetworkParent(ctx, tx, w.actor, NetworkInput{Account: w.Account, Region: w.Region,
		Kind: w.ResourceKind, NetworkSpec: network.NetworkSpec}, network.parentRevision, w.ID)
	if err == nil && parent != network.parentScope {
		return identity.ErrAccountStale
	}
	return err
}

func (s Store) processNetwork(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	creator, ok := client.(provider.NetworkCreator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = w.ResourceKind
	if w.Status == "queued" {
		return s.dispatchNetwork(ctx, task, w, scope, creator)
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	object, err := creator.FindCreatedNetwork(ctx, scope, w.network.NetworkSpec, w.network.token, w.network.resourceID)
	if err == nil && object.ID != "" {
		if w.network.resourceID != "" && object.ID != w.network.resourceID {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_receipt_mismatch", "")
		}
		switch object.Status {
		case "UNBIND", "DOWN":
			if w.ResourceKind != provider.ElasticIP {
				break
			}
			fallthrough
		case "available", "Available", "ACTIVE":
			if !matchesNetworkLinks(w, object.Links) {
				return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
			}
			return false, s.confirmCreation(ctx, task, w, object)
		case "failed", "ERROR":
			return false, s.transition(ctx, task, w.Status, "needs_review", "provider_creation_error", "")
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
	}
	return true, nil
}

func matchesNetworkLinks(w work, links []provider.Link) bool {
	if w.network.ParentID == "" {
		return true
	}
	expected := []provider.Link{networkParent(w.ResourceKind, w.network.ParentID)}
	if w.ResourceKind == provider.Subnet && w.network.Zone != "" {
		expected = append(expected, provider.Link{Kind: provider.Zone, ID: w.network.Zone, Relation: "in_zone"})
	}
	return slices.Equal(links, expected)
}

func (s Store) dispatchNetwork(ctx context.Context, task mq.Task, w work, scope provider.Scope, creator provider.NetworkCreator) (bool, error) {
	var send func(context.Context) (provider.LaunchReceipt, error)
	if preparer, ok := creator.(provider.NetworkCreationPreparer); ok {
		var err error
		send, err = preparer.PrepareNetworkCreation(ctx, scope, w.network.NetworkSpec, w.network.token)
		if err != nil || send == nil || ctx.Err() != nil {
			return s.rejectCreationPreflight(ctx, task, w, err)
		}
	}
	return s.dispatchPreparedResource(ctx, task, w, func(ctx context.Context, armed work) (provider.LaunchReceipt, error) {
		if send != nil {
			return send(ctx)
		}
		return creator.CreateNetwork(ctx, scope, armed.network.NetworkSpec, armed.network.token)
	})
}

func (s Store) dispatchPreparedResource(ctx context.Context, task mq.Task, w work, send func(context.Context, work) (provider.LaunchReceipt, error)) (bool, error) {
	armed, err := s.prepare(ctx, task, true)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, armed)
	}
	if err != nil {
		return false, err
	}
	if armed.Status != "applying" {
		return false, mq.ErrLeaseLost
	}
	receipt, err := send(ctx, armed)
	return s.settleCreationDispatch(ctx, task, w.ResourceKind, receipt, err)
}
