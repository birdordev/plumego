package vsphere

import (
	"context"
	"errors"
	"time"

	"with-cloud-manager/internal/provider"
)

const (
	pageObjects         = 250
	maxCollectorPages   = 1024
	maxCollectorObjects = 100000
)

type propertyValue struct {
	Text       string      `xml:",chardata"`
	References []reference `xml:"ManagedObjectReference"`
}

type collectedObject struct {
	Object     reference `xml:"obj"`
	Properties []struct {
		Name  string        `xml:"name"`
		Value propertyValue `xml:"val"`
	} `xml:"propSet"`
	Missing []struct {
		Path string `xml:"path"`
	} `xml:"missingSet"`
}

type objectSpec struct {
	Target    reference `xml:"obj"`
	Skip      bool      `xml:"skip"`
	Traversal *struct {
		Schema string `xml:"xsi:type,attr"`
		Type   string `xml:"type"`
		Path   string `xml:"path"`
		Skip   bool   `xml:"skip"`
	} `xml:"selectSet,omitempty"`
}

type retrieval struct {
	Target reference `xml:"_this"`
	Spec   struct {
		Properties struct {
			Type  string   `xml:"type"`
			All   bool     `xml:"all"`
			Paths []string `xml:"pathSet"`
		} `xml:"propSet"`
		Objects       []objectSpec `xml:"objectSet"`
		ReportMissing bool         `xml:"reportMissingObjectsInResults"`
	} `xml:"specSet"`
	Options struct {
		MaxObjects int `xml:"maxObjects"`
	} `xml:"options"`
}

type retrieveResponse struct {
	Result *struct {
		Token   string            `xml:"token"`
		Objects []collectedObject `xml:"objects"`
	} `xml:"returnval"`
}

func (c *Client) retrieve(ctx context.Context, kind string, fields []string, targets []objectSpec, visit func(collectedObject) error) error {
	input := retrieval{Target: c.content.Collector}
	input.Spec.Properties.Type, input.Spec.Properties.Paths = kind, fields
	input.Spec.Objects, input.Spec.ReportMissing = targets, true
	input.Options.MaxObjects = pageObjects
	var response retrieveResponse
	if err := c.call(ctx, "RetrievePropertiesEx", input, &response); err != nil {
		return err
	}
	tokenSeen := make(map[string]struct{})
	objectCount := 0
	for page := 0; response.Result != nil; page++ {
		result := response.Result
		objectCount += len(result.Objects)
		if page >= maxCollectorPages || objectCount > maxCollectorObjects || len(result.Objects) > pageObjects ||
			len(result.Token) > 4096 || (result.Token != "" && len(result.Objects) == 0) {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, item := range result.Objects {
			if len(item.Missing) != 0 {
				return &provider.Failure{Kind: provider.Unauthorized}
			}
			if err := visit(item); err != nil {
				return err
			}
		}
		if result.Token == "" {
			return nil
		}
		if _, exists := tokenSeen[result.Token]; exists {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		tokenSeen[result.Token] = struct{}{}
		response = retrieveResponse{}
		if err := c.call(ctx, "ContinueRetrievePropertiesEx", struct {
			Target reference `xml:"_this"`
			Token  string    `xml:"token"`
		}{c.content.Collector, result.Token}, &response); err != nil {
			return err
		}
		if response.Result == nil {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	return nil
}

func (c *Client) verifyDatacenters(ctx context.Context, ids []string) (map[string]provider.Object, error) {
	targets := make([]objectSpec, len(ids))
	expected := make(map[string]struct{}, len(ids))
	for i, id := range ids {
		targets[i] = objectSpec{Target: reference{Type: "Datacenter", ID: id}}
		expected[id] = struct{}{}
	}
	found := make(map[string]provider.Object, len(ids))
	err := c.retrieve(ctx, "Datacenter", []string{"name"}, targets, func(item collectedObject) error {
		_, known := expected[item.Object.ID]
		if !item.Object.valid("Datacenter") || !known || len(item.Properties) != 1 || item.Properties[0].Name != "name" || !validName(item.Properties[0].Value.Text) {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		delete(expected, item.Object.ID)
		found[item.Object.ID] = provider.Object{ID: item.Object.ID, Name: item.Properties[0].Value.Text, Status: "available"}
		return nil
	})
	if err == nil && len(expected) != 0 {
		err = &provider.Failure{Kind: provider.Unauthorized}
	}
	return found, err
}

func (c *Client) retrieveInDatacenter(ctx context.Context, id, kind string, fields []string, visit func(collectedObject) error) (err error) {
	var view struct {
		Target reference `xml:"returnval"`
	}
	if err := c.call(ctx, "CreateContainerView", struct {
		Target    reference `xml:"_this"`
		Container reference `xml:"container"`
		Type      string    `xml:"type"`
		Recursive bool      `xml:"recursive"`
	}{c.content.Views, reference{Type: "Datacenter", ID: id}, kind, true}, &view); err != nil {
		return err
	}
	if !view.Target.valid("ContainerView") {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		err = errors.Join(err, c.call(cleanup, "DestroyView", struct {
			Target reference `xml:"_this"`
		}{view.Target}, new(struct{})))
	}()
	spec := objectSpec{Target: view.Target, Skip: true}
	spec.Traversal = &struct {
		Schema string `xml:"xsi:type,attr"`
		Type   string `xml:"type"`
		Path   string `xml:"path"`
		Skip   bool   `xml:"skip"`
	}{Schema: "TraversalSpec", Type: "ContainerView", Path: "view"}
	return c.retrieve(ctx, kind, fields, []objectSpec{spec}, visit)
}
