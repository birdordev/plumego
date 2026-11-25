package provider

import (
	"fmt"
	"strings"
	"testing"
)

func TestAcceptancePageContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cursor string
		page   Page
		valid  bool
	}{
		{name: "continuation", page: Page{Objects: []Object{{ID: "one"}}, Next: "next"}, valid: true},
		{name: "terminal", cursor: "next", valid: true},
		{name: "repeated object", page: Page{Objects: []Object{{ID: "one"}, {ID: "one"}}}, valid: true},
		{name: "nonadvancing", cursor: "next", page: Page{Next: "next"}},
		{name: "invalid object", page: Page{Objects: []Object{{}}}},
		{name: "oversized cursor", page: Page{Next: strings.Repeat("x", MaxCursorBytes+1)}},
		{name: "oversized page", page: Page{Objects: make([]Object, MaxPageObjects+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.page.Validate(tc.cursor)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !IsFailure(fmt.Errorf("decode page: %w", err), InvalidResponse) {
				t.Fatalf("expected invalid response, got %v", err)
			}
		})
	}
}
