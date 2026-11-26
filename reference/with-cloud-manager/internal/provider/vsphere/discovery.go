package vsphere

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

const maxInventoryImageBytes = 96 << 20

type inventoryImage struct {
	scope       provider.Scope
	objects     []provider.Object
	fingerprint string
}

func (c *Client) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if scope.Provider != provider.VSphere || scope.RemoteIdentity != c.identity {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !SupportsInventory(scope.Region, scope.Kind) {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if scope.Kind != provider.Region {
		if _, ok := c.datacenters[scope.Region]; !ok {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
	}
	start := 0
	var fingerprint string
	if cursor != "" {
		hash, offset, ok := strings.Cut(cursor, ":")
		value, err := strconv.Atoi(offset)
		if !ok || len(hash) != 64 || err != nil || value <= 0 || value >= maxCollectorObjects || value%pageObjects != 0 {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		start, fingerprint = value, hash
	}
	if c.image == nil || c.image.scope != scope || cursor == "" {
		image, err := c.loadImage(ctx, scope)
		if err != nil {
			return provider.Page{}, err
		}
		c.image = image
	}
	if fingerprint != "" && (fingerprint != c.image.fingerprint || start >= len(c.image.objects)) {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	end := min(start+pageObjects, len(c.image.objects))
	page := provider.Page{Objects: c.image.objects[start:end]}
	if end < len(c.image.objects) {
		page.Next = c.image.fingerprint + ":" + strconv.Itoa(end)
	}
	if err := page.Validate(cursor); err != nil {
		return provider.Page{}, err
	}
	return page, nil
}

func SupportsInventory(region string, kind provider.Kind) bool {
	switch kind {
	case provider.Region:
		return region == ""
	case provider.Instance, provider.Image, provider.Network:
		return datacenterID.MatchString(region)
	default:
		return false
	}
}

func (c *Client) loadImage(ctx context.Context, scope provider.Scope) (*inventoryImage, error) {
	image := &inventoryImage{scope: scope, objects: make([]provider.Object, 0)}
	if scope.Kind == provider.Region {
		for _, object := range c.datacenters {
			image.objects = append(image.objects, object)
		}
	} else if err := c.loadDatacenterImage(ctx, image); err != nil {
		return nil, err
	}
	slices.SortFunc(image.objects, func(a, b provider.Object) int { return cmp.Compare(a.ID, b.ID) })
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	if err := encoder.Encode(scope); err != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, object := range image.objects {
		if err := encoder.Encode(object); err != nil {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	image.fingerprint = hex.EncodeToString(hash.Sum(nil))
	return image, nil
}

func (c *Client) loadDatacenterImage(ctx context.Context, image *inventoryImage) error {
	moKind, fields := "VirtualMachine", []string{"name", "config.instanceUuid", "config.template", "runtime.powerState", "network"}
	project := projectVM
	if image.scope.Kind == provider.Network {
		moKind, fields = "Network", []string{"name"}
		project = projectNetwork
	}
	seen := make(map[string]struct{})
	refs := make(map[reference]struct{})
	totalBytes := 0
	return c.retrieveInDatacenter(ctx, image.scope.Region, moKind, fields, func(row collectedObject) error {
		if _, exists := refs[row.Object]; exists {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		refs[row.Object] = struct{}{}
		object, kind, err := project(row)
		if err != nil || kind != image.scope.Kind {
			return err
		}
		if _, exists := seen[object.ID]; exists {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		seen[object.ID] = struct{}{}
		totalBytes += 192 + len(object.ID) + len(object.Name) + len(object.Status)
		for _, link := range object.Links {
			totalBytes += 96 + len(link.ID) + len(link.Relation)
		}
		if totalBytes > maxInventoryImageBytes {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		image.objects = append(image.objects, object)
		return nil
	})
}
