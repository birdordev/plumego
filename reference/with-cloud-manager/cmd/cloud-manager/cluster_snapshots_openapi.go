package main

import "github.com/birdordev/plumego/x/openapi"

func clusterNodePoolsOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	pool := openapi.Schema{
		Type: "object", Properties: map[string]openapi.Schema{
			"id": openapi.String, "name": openapi.String, "type": openapi.String, "status": openapi.String,
			"current_nodes": openapi.Integer, "desired_nodes": openapi.Integer, "min_nodes": openapi.Integer,
			"max_nodes": openapi.Integer, "autoscaling": openapi.Boolean,
		},
	}
	return clusterSnapshotOpenAPI("readProjectClusterNodePools", "节点池", "node_pools", pool, readAuth)
}

func clusterWorkloadsOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	workload := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"uid": openapi.String, "namespace": openapi.String, "kind": openapi.String, "name": openapi.String,
		"desired": openapi.Integer, "ready": openapi.Integer, "active": openapi.Integer,
		"succeeded": openapi.Integer, "failed": openapi.Integer, "suspended": openapi.Boolean,
	}}
	return clusterSnapshotOpenAPI("readProjectClusterWorkloads", "工作负载", "workloads", workload, readAuth)
}

func clusterSnapshotOpenAPI(operation, label, field string, item openapi.Schema, auth []openapi.SecurityRequirement) openapi.Op {
	page := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"snapshot": {Type: "string", Format: "uuid"}, "observed_at": {Type: "string", Format: "date-time"},
		field: {Type: "array", Items: &item}, "total": openapi.Integer, "next_offset": openapi.Integer,
	},
	}
	return openapi.Op{
		OperationID: operation, Security: auth, Summary: "分页读取 Kubernetes 集群的完整" + label + "快照",
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "region", In: "query", Required: true, Schema: openapi.String},
			{Name: "snapshot", In: "query", Description: "响应提供的快照标识。offset 大于零时必填。", Schema: openapi.String},
			{Name: "offset", In: "query", Description: "从零开始的行偏移", Schema: openapi.Integer},
			{Name: "limit", In: "query", Description: "每页 1 至 100 条，默认 50", Schema: openapi.Integer},
		},
		Responses: map[string]openapi.Response{
			"200": {Description: label + "页、快照标识与观测时间", Content: map[string]openapi.MediaType{
				"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{"data": page}}},
			}},
			"400": {Description: "查询参数无效"}, "401": {Description: "服务凭据无效"},
			"403": {Description: "项目不可访问"}, "404": {Description: "父集群或完整快照不可见"},
			"409": {Description: "快照已替换，请从第一页重新读取"},
			"503": {Description: "资产存储不可用"},
		},
	}
}
