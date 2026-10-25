package gateway

import webspinner "github.com/webong/gateway/pkg/web/spinner"

type Edge = webspinner.Edge

func NewEdge(planner Planner, executor Executor, maxBodySize int64) *Edge {
	return webspinner.NewEdge(planner, executor, maxBodySize)
}
