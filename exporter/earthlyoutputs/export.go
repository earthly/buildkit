package earthlyoutputs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	archiveexporter "github.com/containerd/containerd/images/archive"
	"github.com/containerd/containerd/leases"
	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/exporter"
	"github.com/moby/buildkit/exporter/containerimage"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/util/contentutil"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/progress"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
)

type ExporterVariant string

const (
	keyImageName        = "name"
	keyLayerCompression = "compression"
	VariantOCI          = "oci"
	VariantDocker       = "docker"
	ociTypes            = "oci-mediatypes"
)

type Opt struct {
	SessionManager *session.Manager
	ImageWriter    *containerimage.ImageWriter
	Variant        ExporterVariant
	LeaseManager   leases.Manager
}

type imageExporter struct {
	opt Opt
}

func New(opt Opt) (exporter.Exporter, error) {
	im := &imageExporter{opt: opt}
	return im, nil
}

func (e *imageExporter) Resolve(ctx context.Context, opt map[string]string) (exporter.ExporterInstance, error) {
	var ot *bool
	i := &imageExporterInstance{
		imageExporter:    e,
		layerCompression: compression.Default,
	}
	for k, v := range opt {
		switch k {
		case keyImageName:
			i.name = v
		case keyLayerCompression:
			switch v {
			case "gzip":
				i.layerCompression = compression.Gzip
			case "uncompressed":
				i.layerCompression = compression.Uncompressed
			default:
				return nil, errors.Errorf("unsupported layer compression type: %v", v)
			}
		case ociTypes:
			ot = new(bool)
			if v == "" {
				*ot = true
				continue
			}
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, errors.Wrapf(err, "non-bool value specified for %s", k)
			}
			*ot = b
		default:
			if i.meta == nil {
				i.meta = make(map[string][]byte)
			}
			i.meta[k] = []byte(v)
		}
	}
	if ot == nil {
		i.ociTypes = e.opt.Variant == VariantOCI
	} else {
		i.ociTypes = *ot
	}
	return i, nil
}

type imageExporterInstance struct {
	*imageExporter
	meta             map[string][]byte
	name             string
	ociTypes         bool
	layerCompression compression.Type
}

func (e *imageExporterInstance) Name() string {
	return "exporting to oci image format"
}

func (e *imageExporterInstance) Export(ctx context.Context, src exporter.Source, sessionID string) (map[string]string, error) {
	if src.Ref != nil {
		return nil, errors.Errorf("export with src.Ref not supported")
	}
	if src.Metadata == nil {
		src.Metadata = make(map[string][]byte)
	}
	for k, v := range e.meta {
		src.Metadata[k] = v
	}
	numExportsStr, ok := src.Metadata["earthly-num-exports"]
	if !ok {
		return nil, errors.New("earthly-num-exports key not found")
	}
	numExports, err := strconv.Atoi(string(numExportsStr))
	if err != nil {
		return nil, errors.Wrap(err, "parse earthly-num-exports")
	}
	expSrcs := make([]exporter.Source, 0, numExports)
	for i := 0; i < numExports; i++ {
		refKey := fmt.Sprintf("earthly-%d", i)
		expRef, found := src.Refs[refKey]
		if !found {
			return nil, errors.Errorf("key %s not found in refs", refKey)
		}
		expMd := make(map[string][]byte)
		for k, v := range src.Metadata {
			expMd[k] = v
		}
		name := src.Metadata[fmt.Sprintf("image.name/%d", i)]
		expMd["image.name"] = name
		imgConfig := src.Metadata[fmt.Sprintf("containerimage.config/%d", i)]
		expMd["containerimage.config"] = imgConfig
		expSrc := exporter.Source{
			Ref:      expRef,
			Refs:     map[string]cache.ImmutableRef{},
			Metadata: expMd,
		}
		expSrcs = append(expSrcs, expSrc)
	}

	ctx, done, err := leaseutil.WithLease(ctx, e.opt.LeaseManager, leaseutil.MakeTemporary)
	if err != nil {
		return nil, err
	}
	defer done(context.TODO())

	descs := make([]*ocispec.Descriptor, 0, numExports)
	names := make([][]string, 0, numExports)
	for _, expSrc := range expSrcs {
		desc, err := e.opt.ImageWriter.Commit(ctx, expSrc, e.ociTypes, e.layerCompression)
		if err != nil {
			return nil, err
		}
		defer func() {
			e.opt.ImageWriter.ContentStore().Delete(context.TODO(), desc.Digest)
		}()
		if desc.Annotations == nil {
			desc.Annotations = map[string]string{}
		}
		desc.Annotations[ocispec.AnnotationCreated] = time.Now().UTC().Format(time.RFC3339)
		descs = append(descs, desc)

		imgName := e.name
		if n, ok := expSrc.Metadata["image.name"]; ok {
			imgName = string(n)
		}
		imgNames, err := normalizedNames(imgName)
		if err != nil {
			return nil, err
		}
		names = append(names, imgNames)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	caller, err := e.opt.SessionManager.Get(timeoutCtx, sessionID, false)
	if err != nil {
		return nil, err
	}

	resp := make(map[string]string)
	resp["earthly-num-exports"] = fmt.Sprintf("%d", numExports)
	for index, desc := range descs {
		resp[fmt.Sprintf("containerimage.digest/%d", index)] = desc.Digest.String()
		if len(names[index]) != 0 {
			resp[fmt.Sprintf("image.name/%d", index)] = strings.Join(names[index], ",")
		}
	}

	writers, err := filesync.MultiCopyFileWriter(ctx, resp, caller, numExports)
	if err != nil {
		return nil, err
	}

	mprovider := contentutil.NewMultiProvider(e.opt.ImageWriter.ContentStore())
	for _, r := range src.Refs {
		remote, err := r.GetRemote(ctx, false, e.layerCompression)
		if err != nil {
			return nil, err
		}
		if unlazier, ok := remote.Provider.(cache.Unlazier); ok {
			if err := unlazier.Unlazy(ctx); err != nil {
				return nil, err
			}
		}
		for _, desc := range remote.Descriptors {
			mprovider.Add(desc.Digest, remote.Provider)
		}
	}

	report := oneOffProgress(ctx, "sending tarballs")
	for index, desc := range descs {
		w := writers[index]
		expOpts := []archiveexporter.ExportOpt{archiveexporter.WithManifest(*desc, names[index]...)}
		switch e.opt.Variant {
		case VariantOCI:
			expOpts = append(expOpts, archiveexporter.WithAllPlatforms(), archiveexporter.WithSkipDockerManifest())
		case VariantDocker:
		default:
			return nil, report(errors.Errorf("invalid variant %q", e.opt.Variant))
		}
		if err := archiveexporter.Export(ctx, mprovider, w, expOpts...); err != nil {
			w.Close()
			if grpcerrors.Code(err) == codes.AlreadyExists {
				return resp, report(nil)
			}
			return nil, report(err)
		}
		err = w.Close()
		// TODO: This is not supported in the multi variant.
		// if grpcerrors.Code(err) == codes.AlreadyExists {
		// 	return resp, report(nil)
		// }
		if err != nil {
			return nil, report(err)
		}
	}
	return resp, report(err)
}

func oneOffProgress(ctx context.Context, id string) func(err error) error {
	pw, _, _ := progress.FromContext(ctx)
	now := time.Now()
	st := progress.Status{
		Started: &now,
	}
	pw.Write(id, st)
	return func(err error) error {
		// TODO: set error on status
		now := time.Now()
		st.Completed = &now
		pw.Write(id, st)
		pw.Close()
		return err
	}
}

func normalizedNames(name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	names := strings.Split(name, ",")
	var tagNames = make([]string, len(names))
	for i, name := range names {
		parsed, err := reference.ParseNormalizedNamed(name)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse %s", name)
		}
		tagNames[i] = reference.TagNameOnly(parsed).String()
	}
	return tagNames, nil
}
