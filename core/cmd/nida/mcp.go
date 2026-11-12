package main

import (
	"context"
	"io"
	"os"

	"github.com/fathorMB/NidaOS/core/internal/mcp"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

// mcpServe runs the MCP server on stdin and stdout (milestone M-09). The
// agent starts "nida mcp" with NIDA_SERVER and NIDA_TOKEN (an agent
// token). Through the local socket, the server has the identity "local".
func (a *app) mcpServe(ctx context.Context, _ []string, in io.Reader) error {
	s := &mcp.Server{API: a.c, Version: version.Version, Instructions: mcp.Instructions}
	return s.Serve(ctx, in, a.out)
}

var stdin io.Reader = os.Stdin
