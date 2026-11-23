package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/leofelipet/contexta/internal/mcpkeys"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// KeyAuthenticator resolves a token hash to an unexpired key.
type KeyAuthenticator interface {
	AuthenticateMCPKey(ctx context.Context, hash string) (mcpkeys.Key, error)
}

// noExpiry stands in for keys without expiry: the SDK rejects a zero Expiration.
const noExpiry = 100 * 365 * 24 * time.Hour

func verifier(keys KeyAuthenticator, catalog []mcpkeys.Tool) auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		key, err := keys.AuthenticateMCPKey(ctx, mcpkeys.Hash(token))
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, auth.ErrInvalidToken
		}
		if err != nil {
			return nil, err
		}
		info := &auth.TokenInfo{UserID: key.ID, Expiration: time.Now().Add(noExpiry)}
		if key.ExpiresAt != nil {
			info.Expiration = *key.ExpiresAt
		}
		for _, tool := range catalog {
			if key.Allows(tool) {
				info.Scopes = append(info.Scopes, tool.Name)
			}
		}
		return info, nil
	}
}

// authorizeTools hides and blocks tools outside the calling key's scope.
func authorizeTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		var scopes []string
		if extra := req.GetExtra(); extra != nil && extra.TokenInfo != nil {
			scopes = extra.TokenInfo.Scopes
		}
		switch method {
		case "tools/call":
			name := req.(*mcp.CallToolRequest).Params.Name
			if !slices.Contains(scopes, name) {
				return nil, fmt.Errorf("this key is not allowed to call %s", name)
			}
		case "tools/list":
			result, err := next(ctx, method, req)
			if list, ok := result.(*mcp.ListToolsResult); ok {
				list.Tools = slices.DeleteFunc(list.Tools, func(tool *mcp.Tool) bool { return !slices.Contains(scopes, tool.Name) })
			}
			return result, err
		}
		return next(ctx, method, req)
	}
}

// listTools reads the registered tools back through an in-memory client.
func listTools(server *mcp.Server) ([]mcpkeys.Tool, error) {
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "catalog"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	tools := make([]mcpkeys.Tool, 0)
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		tools = append(tools, mcpkeys.Tool{Name: tool.Name, Description: tool.Description, ReadOnly: readOnly})
	}
	return tools, nil
}
