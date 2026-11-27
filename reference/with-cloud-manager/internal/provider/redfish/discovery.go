package redfish

import (
	"context"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

const maxCollectionPages = 64

type collection struct {
	Members []link `json:"Members"`
	Count   *int   `json:"Members@odata.count"`
	Next    string `json:"Members@odata.nextLink"`
}

type system struct {
	link
	Boot    bootOverride `json:"Boot"`
	etag    string
	UUID    string `json:"UUID"`
	Name    string `json:"Name"`
	Type    string `json:"SystemType"`
	Power   string `json:"PowerState"`
	Actions struct {
		Reset resetAction `json:"#ComputerSystem.Reset"`
	} `json:"Actions"`
	Status struct {
		State string `json:"State"`
	} `json:"Status"`
}

func SupportsInventory(region string, kind provider.Kind) bool {
	return (kind == provider.Region && region == "") || (kind == provider.BareMetal && siteID.MatchString(region))
}

func (c *Client) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if scope.Provider != provider.Redfish || scope.RemoteIdentity != c.Identity() {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !SupportsInventory(scope.Region, scope.Kind) {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if scope.Kind == provider.BareMetal && scope.Region != c.config.Site {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if cursor != "" {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	if scope.Kind == provider.Region {
		return provider.Page{Objects: []provider.Object{{ID: c.config.Site, Name: c.config.Site, Status: "available"}}}, nil
	}
	return c.readSystems(ctx)
}

func (c *Client) readSystems(ctx context.Context) (provider.Page, error) {
	var page provider.Page
	err := c.walkSystems(ctx, func(_ *url.URL, machine system) {
		page.Objects = append(page.Objects, provider.Object{ID: strings.ToLower(machine.UUID), Name: machine.Name, Status: machine.status()})
	})
	if err != nil {
		return provider.Page{}, err
	}
	return page, page.Validate("")
}

func (c *Client) walkSystems(ctx context.Context, observe func(*url.URL, system)) error {
	target := &url.URL{Path: c.systems}
	pages := map[string]bool{}
	members := map[string]bool{}
	ids := map[string]bool{}
	expected := -1
	for {
		key := target.String()
		if pages[key] || len(pages) >= maxCollectionPages {
			return invalidResponse()
		}
		pages[key] = true
		var list collection
		if err := c.get(ctx, target, &list); err != nil {
			return err
		}
		if list.Count == nil || list.Members == nil || len(members)+len(list.Members) > provider.MaxPageObjects {
			return invalidResponse()
		}
		if list.Count != nil {
			if *list.Count < 0 || *list.Count > provider.MaxPageObjects || (expected >= 0 && *list.Count != expected) {
				return invalidResponse()
			}
			expected = *list.Count
		}
		for _, member := range list.Members {
			resource, err := relativeLink(member.ID)
			if err != nil || resource.RawQuery != "" || strings.TrimSuffix(resource.Path, "/") == c.systems {
				return invalidResponse()
			}
			path := strings.TrimSuffix(resource.Path, "/")
			if members[path] {
				return invalidResponse()
			}
			members[path] = true
			machine, err := c.readSystem(ctx, resource)
			if err != nil {
				return err
			}
			switch machine.Type {
			case "Physical":
			case "Virtual", "PhysicallyPartitioned", "VirtuallyPartitioned", "Composed", "OS", "DPU":
				continue
			default:
				return invalidResponse()
			}
			id := strings.ToLower(machine.UUID)
			if !validUUID(id) || ids[id] || machine.Name == "" || strings.ContainsRune(machine.Name, 0) {
				return invalidResponse()
			}
			ids[id] = true
			observe(resource, machine)
		}
		if list.Next == "" {
			if expected >= 0 && expected != len(members) {
				return invalidResponse()
			}
			return nil
		}
		next, err := relativeLink(list.Next)
		if err != nil || strings.TrimSuffix(next.Path, "/") != c.systems || next.RawQuery == "" {
			return invalidResponse()
		}
		target = next
	}
}

func (s system) status() string {
	if s.Status.State == "Absent" || s.Status.State == "Disabled" {
		return strings.ToLower(s.Status.State)
	}
	switch s.Power {
	case "On":
		return "on"
	case "Off":
		return "off"
	case "PoweringOn":
		return "powering_on"
	case "PoweringOff":
		return "powering_off"
	default:
		return "unknown"
	}
}
