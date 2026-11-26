package vsphere

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func projectVM(row collectedObject) (provider.Object, provider.Kind, error) {
	failure := &provider.Failure{Kind: provider.InvalidResponse}
	if !row.Object.valid("VirtualMachine") || len(row.Properties) > 5 {
		return provider.Object{}, "", failure
	}
	fields := make(map[string]propertyValue, len(row.Properties))
	for _, field := range row.Properties {
		if _, exists := fields[field.Name]; exists {
			return provider.Object{}, "", failure
		}
		fields[field.Name] = field.Value
	}
	id, name := strings.ToLower(fields["config.instanceUuid"].Text), fields["name"].Text
	template, err := strconv.ParseBool(fields["config.template"].Text)
	if err != nil || !uuid.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" || !validName(name) {
		return provider.Object{}, "", failure
	}
	item := provider.Object{ID: id, Name: name, Status: "available"}
	if template {
		return item, provider.Image, nil
	}
	switch fields["runtime.powerState"].Text {
	case "poweredOn":
		item.Status = "running"
	case "poweredOff":
		item.Status = "stopped"
	case "suspended":
		item.Status = "suspended"
	default:
		return provider.Object{}, "", failure
	}
	networks := fields["network"].References
	if len(networks) > 256 {
		return provider.Object{}, "", failure
	}
	seen := make(map[string]struct{}, len(networks))
	for _, ref := range networks {
		if !isNetwork(ref) {
			return provider.Object{}, "", failure
		}
		if _, ok := seen[ref.ID]; !ok {
			seen[ref.ID] = struct{}{}
			item.Link(provider.Network, ref.ID, "in_network")
		}
	}
	slices.SortFunc(item.Links, func(a, b provider.Link) int { return cmp.Compare(a.ID, b.ID) })
	return item, provider.Instance, nil
}

func projectNetwork(row collectedObject) (provider.Object, provider.Kind, error) {
	if !isNetwork(row.Object) || len(row.Properties) != 1 || row.Properties[0].Name != "name" || !validName(row.Properties[0].Value.Text) {
		return provider.Object{}, "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.Object{ID: row.Object.ID, Name: row.Properties[0].Value.Text, Status: "available"}, provider.Network, nil
}

func isNetwork(ref reference) bool {
	switch ref.Type {
	case "Network", "DistributedVirtualPortgroup", "OpaqueNetwork":
		return referenceID.MatchString(ref.ID)
	default:
		return false
	}
}

func validName(name string) bool {
	return strings.TrimSpace(name) != "" && len(name) <= 1024
}
