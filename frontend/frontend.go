package frontend

import (
	"context"

	"github.com/moby/buildkit/cache" // earthly-specific this import breaks the mips64 frontend tests due to incorrect types on inode
	"github.com/moby/buildkit/client/llb"
	gw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/solver/result"
	digest "github.com/opencontainers/go-digest"
)

type Result = result.Result[solver.ResultProxy]

type Attestation = result.Attestation[solver.ResultProxy]

type Frontend interface {
	Solve(ctx context.Context, llb FrontendLLBBridge, opt map[string]string, inputs map[string]*pb.Definition, sid string, sm *session.Manager) (*Result, error)
}

type FrontendLLBBridge interface {
	Solve(ctx context.Context, req SolveRequest, sid string) (*Result, error)
	Export(ctx context.Context, refs map[string]cache.ImmutableRef, metadata map[string][]byte) error // earthly-specific
	ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (string, digest.Digest, []byte, error)
	Warn(ctx context.Context, dgst digest.Digest, msg string, opts WarnOpts) error
}

type SolveRequest = gw.SolveRequest

type CacheOptionsEntry = gw.CacheOptionsEntry

type WarnOpts = gw.WarnOpts
