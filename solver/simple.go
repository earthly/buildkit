package solver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"sync"
	"time"

	"github.com/moby/buildkit/util/progress"
	"github.com/moby/buildkit/util/tracing"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

type simpleSolver struct {
	resolveOpFunc ResolveOpFunc
	solver        *Solver
	job           *Job
}

var cache = map[string]Result{}
var cacheMaps = map[string]*simpleCacheMap{}
var execInProgress = map[string]struct{}{}
var mu = sync.Mutex{}

type cacheMapDep struct {
	selector string
	computed string
}

type simpleCacheMap struct {
	digest string
	inputs []string
	deps   []cacheMapDep
}

func (s *simpleSolver) build(ctx context.Context, e Edge) (CachedResult, error) {

	// Ordered list of vertices to build.
	digests, vertices := s.exploreVertices(e)

	var ret Result

	for _, d := range digests {
		fmt.Println()

		vertex, ok := vertices[d]
		if !ok {
			return nil, errors.Errorf("digest %s not found", d)
		}

		mu.Lock()
		// TODO: replace busy-wait loop with a wait-for-channel-to-close approach
		for {
			if _, shouldWait := execInProgress[d.String()]; !shouldWait {
				execInProgress[d.String()] = struct{}{}
				mu.Unlock()
				break
			}
			mu.Unlock()
			time.Sleep(time.Millisecond * 10)
			mu.Lock()
		}

		st := s.createState(vertex)

		op := newSharedOp(st.opts.ResolveOpFunc, st.opts.DefaultCache, st)

		// Required to access cache map results on state.
		st.op = op

		// CacheMap populates required fields in SourceOp.
		cm, err := op.CacheMap(ctx, int(e.Index))
		if err != nil {
			return nil, err
		}

		inputs, err := s.preprocessInputs(ctx, st, vertex, cm.CacheMap)
		if err != nil {
			return nil, err
		}

		cacheKey, err := s.cacheKey(ctx, d.String())
		if err != nil {
			return nil, err
		}

		mu.Lock()
		if v, ok := cache[cacheKey]; ok && v != nil {
			delete(execInProgress, d.String())
			mu.Unlock()
			ctx = progress.WithProgress(ctx, st.mpw)
			notifyCompleted := notifyStarted(ctx, &st.clientVertex, true)
			notifyCompleted(nil, true)
			ret = v
			continue
		}
		mu.Unlock()

		results, _, err := op.Exec(ctx, inputs)
		if err != nil {
			mu.Lock()
			delete(execInProgress, d.String())
			mu.Unlock()
			return nil, err
		}

		res := results[int(e.Index)]
		ret = res

		mu.Lock()
		delete(execInProgress, d.String())
		cache[cacheKey] = res
		mu.Unlock()
	}

	return NewCachedResult(ret, []ExportableCacheKey{}), nil
}

// createState creates a new state struct with required and placeholder values.
func (s *simpleSolver) createState(vertex Vertex) *state {
	defaultCache := NewInMemoryCacheManager()

	st := &state{
		opts:         SolverOpt{DefaultCache: defaultCache, ResolveOpFunc: s.resolveOpFunc},
		parents:      map[digest.Digest]struct{}{},
		childVtx:     map[digest.Digest]struct{}{},
		allPw:        map[progress.Writer]struct{}{},
		mpw:          progress.NewMultiWriter(progress.WithMetadata("vertex", vertex.Digest())),
		mspan:        tracing.NewMultiSpan(),
		vtx:          vertex,
		clientVertex: initClientVertex(vertex),
		edges:        map[Index]*edge{},
		index:        s.solver.index,
		mainCache:    defaultCache,
		cache:        map[string]CacheManager{},
		solver:       s.solver,
		origDigest:   vertex.Digest(),
	}

	st.jobs = map[*Job]struct{}{
		s.job: {},
	}

	st.mpw.Add(s.job.pw)

	return st
}

func (s *simpleSolver) preprocessInputs(ctx context.Context, st *state, vertex Vertex, cm *CacheMap) ([]Result, error) {
	// This struct is used to reconstruct a cache key from an LLB digest & all
	// parents using consistent digests that depend on the full dependency chain.
	// TODO: handle cm.Opts (CacheOpts)?
	scm := simpleCacheMap{
		digest: cm.Digest.String(),
		deps:   make([]cacheMapDep, len(cm.Deps)),
		inputs: make([]string, len(cm.Deps)),
	}

	var inputs []Result

	for i, in := range vertex.Inputs() {
		// Compute a cache key given the LLB digest value.
		cacheKey, err := s.cacheKey(ctx, in.Vertex.Digest().String())
		if err != nil {
			return nil, err
		}

		// Lookup the result for that cache key.
		mu.Lock()
		res, ok := cache[cacheKey]
		mu.Unlock()
		if !ok {
			return nil, errors.Errorf("cache key not found: %s", cacheKey)
		}

		dep := cm.Deps[i]

		// Unlazy the result.
		if dep.PreprocessFunc != nil {
			err = dep.PreprocessFunc(ctx, res, st)
			if err != nil {
				return nil, err
			}
		}

		// Add selectors (usually file references) to the struct.
		scm.deps[i] = cacheMapDep{
			selector: dep.Selector.String(),
		}

		// ComputeDigestFunc will usually checksum files. This is then used as
		// part of the cache key to ensure it's consistent & distinct for this
		// operation.
		if dep.ComputeDigestFunc != nil {
			compDigest, err := dep.ComputeDigestFunc(ctx, res, st)
			if err != nil {
				return nil, err
			}
			scm.deps[i].computed = compDigest.String()
		}

		// Add input references to the struct as to link dependencies.
		scm.inputs[i] = in.Vertex.Digest().String()

		// Add the cached result to the input set. These inputs are used to
		// reconstruct dependencies (mounts, etc.) for a new container run.
		inputs = append(inputs, res)
	}

	mu.Lock()
	cacheMaps[vertex.Digest().String()] = &scm
	mu.Unlock()

	return inputs, nil
}

// cacheKey recursively generates a cache key based on a sequence of ancestor
// operations & their cacheable values.
func (s *simpleSolver) cacheKey(ctx context.Context, d string) (string, error) {
	h := sha256.New()

	err := s.cacheKeyRecurse(ctx, d, h)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (s *simpleSolver) cacheKeyRecurse(ctx context.Context, d string, h hash.Hash) error {
	mu.Lock()
	c, ok := cacheMaps[d]
	mu.Unlock()
	if !ok {
		return errors.New("missing cache map key")
	}

	for _, in := range c.inputs {
		err := s.cacheKeyRecurse(ctx, in, h)
		if err != nil {
			return err
		}
	}

	io.WriteString(h, c.digest)
	for _, dep := range c.deps {
		if dep.selector != "" {
			io.WriteString(h, dep.selector)
		}
		if dep.computed != "" {
			io.WriteString(h, dep.computed)
		}
	}

	return nil
}

func (s *simpleSolver) exploreVertices(e Edge) ([]digest.Digest, map[digest.Digest]Vertex) {

	digests := []digest.Digest{e.Vertex.Digest()}
	vertices := map[digest.Digest]Vertex{
		e.Vertex.Digest(): e.Vertex,
	}

	for _, edge := range e.Vertex.Inputs() {
		d, v := s.exploreVertices(edge)
		digests = append(d, digests...)
		for key, value := range v {
			vertices[key] = value
		}
	}

	ret := []digest.Digest{}
	m := map[digest.Digest]struct{}{}
	for _, d := range digests {
		if _, ok := m[d]; !ok {
			ret = append(ret, d)
			m[d] = struct{}{}
		}
	}

	return ret, vertices
}
