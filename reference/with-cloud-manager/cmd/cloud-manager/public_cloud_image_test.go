package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"with-cloud-manager/internal/provider"
)

func (f publicCloudResourceFixture) serveImages(t *testing.T, w http.ResponseWriter, r *http.Request, wrongScope bool) bool {
	t.Helper()
	fail := func() bool {
		t.Errorf("incorrect image inventory request %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	var response any
	id := f.id(provider.Image)
	switch {
	case r.Host == "cvm.tencentcloudapi.com" && r.Header.Get("X-TC-Action") == "DescribeImages":
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Version") != "2017-03-12" {
			return fail()
		}
		var page struct {
			Offset, Limit int
			Filters       []struct {
				Name   string
				Values []string
			}
		}
		if json.Unmarshal(body, &page) != nil || page.Limit != 100 || (page.Offset != 0 && page.Offset != 1) ||
			len(page.Filters) != 1 || page.Filters[0].Name != "image-type" || !slices.Equal(page.Filters[0].Values, []string{"PRIVATE_IMAGE"}) {
			return fail()
		}
		imageType := "PRIVATE_IMAGE"
		if wrongScope {
			imageType = "SHARED_IMAGE"
		}
		if page.Offset == 1 {
			id = "img-2"
		}
		row := map[string]any{"ImageId": id, "ImageName": "fixture-image", "ImageState": "NORMAL",
			"ImageType": imageType, "Tags": []any{map[string]string{"Key": "env", "Value": "test"}}}
		response = map[string]any{"Response": map[string]any{"TotalCount": 2, "ImageSet": []any{row}}}
	case r.Host == "ecs.cn-hangzhou.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "DescribeImages":
		q := r.URL.Query()
		number, err := strconv.Atoi(q.Get("PageNumber"))
		if err != nil || (number != 1 && number != 2) || q.Get("ImageOwnerAlias") != "self" || q.Get("PageSize") != "100" ||
			q.Get("RegionId") != f.region || !validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			return fail()
		}
		owner := "self"
		if wrongScope {
			owner = "others"
		}
		first := (number-1)*100 + 1
		rows := make([]any, 0, 100)
		for i := first; i <= min(first+99, 101); i++ {
			rows = append(rows, map[string]string{"ImageId": fmt.Sprintf("m-%d", i), "ImageName": "fixture-image",
				"Status": "Available", "ImageOwnerAlias": owner})
		}
		response = map[string]any{"RegionId": f.region, "PageNumber": number, "PageSize": 100,
			"TotalCount": 101, "Images": map[string]any{"Image": rows}}
	case r.Host == "ims.cn-north-4.myhuaweicloud.com":
		q := r.URL.Query()
		marker := q.Get("marker")
		if r.Method != http.MethodGet || r.URL.Path != "/v2/cloudimages" ||
			r.Header.Get("X-Auth-Token") != f.token || q.Get("owner") != f.project || q.Get("limit") != "100" ||
			(marker != "" && marker != id) {
			return fail()
		}
		owner := f.project
		if wrongScope {
			owner = "00000000000000000000000000000000"
		}
		page := map[string]string{}
		if marker == "" {
			page["next_marker"] = id
		} else {
			id = "50000000-0000-4000-8000-000000000002"
		}
		response = map[string]any{"page_info": page, "images": []any{map[string]string{
			"id": id, "name": "fixture-image", "status": "active", "owner": owner,
		}}}
	default:
		return false
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
	return true
}
