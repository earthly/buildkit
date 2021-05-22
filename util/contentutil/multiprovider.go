package contentutil

import (
	"context"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// NewMultiProvider creates a new mutable provider with a base provider
func NewMultiProvider(base content.Provider) *MultiProvider {
	return &MultiProvider{
		base: base,
		sub:  map[digest.Digest]content.Provider{},
	}
}

// MultiProvider is a provider backed by a mutable map of providers
type MultiProvider struct {
	mu          sync.RWMutex
	base        content.Provider
	sub         map[digest.Digest]content.Provider
	DebugOutput bool // @#
}

// ReaderAt returns a content.ReaderAt
func (mp *MultiProvider) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	if mp.DebugOutput { // @#
		logrus.New().Info("@#@##@#@ multiprovider readerAt ", desc.Digest.String())
	}
	mp.mu.RLock()
	if p, ok := mp.sub[desc.Digest]; ok {
		mp.mu.RUnlock()
		if mp.DebugOutput && (desc.Digest.String() == "sha256:e20382ccefef84e7247757b42e76d2585ddc6399ef3302e3e258630ad8e1a069" || desc.Digest.String() == "sha256:5a9ca90ca405783e068f19ccc1827d1c10c32095bb1c3c819f37138b85593544") { // @#
			ra, err := p.ReaderAt(ctx, desc)
			if err != nil {
				panic(err)
			}
			dt := make([]byte, ra.Size())
			_, err = ra.ReadAt(dt, 0)
			if err != nil {
				panic(err)
			}
			logrus.New().Info("@#@##@#@       ------> ", string(dt))
		}
		return p.ReaderAt(ctx, desc)
	}
	mp.mu.RUnlock()
	if mp.base == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "content %v", desc.Digest)
	}
	if mp.DebugOutput && (desc.Digest.String() == "sha256:e20382ccefef84e7247757b42e76d2585ddc6399ef3302e3e258630ad8e1a069" || desc.Digest.String() == "sha256:5a9ca90ca405783e068f19ccc1827d1c10c32095bb1c3c819f37138b85593544") { // @#
		ra, err := mp.base.ReaderAt(ctx, desc)
		if err != nil {
			panic(err)
		}
		dt := make([]byte, ra.Size())
		_, err = ra.ReadAt(dt, 0)
		if err != nil {
			panic(err)
		}
		logrus.New().Info("@#@##@#@       ------> ", string(dt))
	}
	return mp.base.ReaderAt(ctx, desc)
}

// Add adds a new child provider for a specific digest
func (mp *MultiProvider) Add(dgst digest.Digest, p content.Provider) {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	mp.sub[dgst] = p
}
