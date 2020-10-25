package earthlyoutputs

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	archiveexporter "github.com/containerd/containerd/images/archive"
	"github.com/containerd/containerd/leases"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/exporter"
	"github.com/moby/buildkit/exporter/containerimage"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/util/contentutil"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/progress"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
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
	return "earthly exporting to oci image format"
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
	numImagesStr, ok := src.Metadata["earthly-num-images"]
	if !ok {
		return nil, errors.New("earthly-num-images key not found")
	}
	numImages, err := strconv.Atoi(string(numImagesStr))
	if err != nil {
		return nil, errors.Wrap(err, "parse earthly-num-images")
	}
	numDirsStr, ok := src.Metadata["earthly-num-dirs"]
	if !ok {
		return nil, errors.New("earthly-num-dirs key not found")
	}
	numDirs, err := strconv.Atoi(string(numDirsStr))
	if err != nil {
		return nil, errors.Wrap(err, "parse earthly-num-dirs")
	}
	numExports := numImages + numDirs
	expSrcs := make([]exporter.Source, 0, numExports)
	for i := 0; i < numImages; i++ {
		refKey := fmt.Sprintf("earthly-image-%d", i)
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
	for i := 0; i < numDirs; i++ {
		refKey := fmt.Sprintf("earthly-dir-%d", i)
		expRef, found := src.Refs[refKey]
		if !found {
			return nil, errors.Errorf("key %s not found in refs", refKey)
		}
		expMd := make(map[string][]byte)
		for k, v := range src.Metadata {
			expMd[k] = v
		}
		earthlyArtifact := src.Metadata[fmt.Sprintf("earthly-artifact/%d", i)]
		expMd["earthly-artifact"] = earthlyArtifact
		srcPath := src.Metadata[fmt.Sprintf("earthly-src-path/%d", i)]
		expMd["earthly-src-path"] = srcPath
		destPath := src.Metadata[fmt.Sprintf("earthly-dest-path/%d", i)]
		expMd["earthly-dest-path"] = destPath
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
		resp[fmt.Sprintf("containerimage.digest-%d", index)] = desc.Digest.String()
		if len(names[index]) != 0 {
			resp[fmt.Sprintf("image.name-%d", index)] = strings.Join(names[index], ",")
		}
	}

	writers := make([]io.WriteCloser, 0, numImages)
	for i := 0; i < numImages; i++ {
		md := make(map[string]string)
		md["earthly-image-index"] = fmt.Sprintf("%d", i)
		md["containerimage.digest"] = descs[i].Digest.String()
		if len(names[i]) != 0 {
			md["image.name"] = strings.Join(names[i], ",")
		}

		w, err := filesync.CopyFileWriter(ctx, md, caller)
		if err != nil {
			return nil, err
		}
		writers = append(writers, w)
	}
	eg, egCtx := errgroup.WithContext(ctx)
	for i := 0; i < numDirs; i++ {
		md := make(map[string]string)
		md["earthly-dir-index"] = fmt.Sprintf("%d", i)
		earthlyArtifact := src.Metadata[fmt.Sprintf("earthly-artifact/%d", i)]
		md["earthly-artifact"] = string(earthlyArtifact)
		srcPath := src.Metadata[fmt.Sprintf("earthly-src-path/%d", i)]
		md["earthly-src-path"] = string(srcPath)
		destPath := src.Metadata[fmt.Sprintf("earthly-dest-path/%d", i)]
		md["earthly-dest-path"] = string(destPath)
		md["containerimage.digest"] = descs[numImages+i].Digest.String()
		eg.Go(exportDirFunc(egCtx, md, caller, expSrcs[numImages+i].Ref))
	}

	mproviders := make([]*contentutil.MultiProvider, 0, numImages)
	for _, expSrc := range expSrcs {
		mprovider := contentutil.NewMultiProvider(e.opt.ImageWriter.ContentStore())
		remote, err := expSrc.Ref.GetRemote(ctx, false, e.layerCompression)
		if err != nil {
			return nil, err
		}
		// unlazy before tar export as the tar writer does not handle
		// layer blobs in parallel (whereas unlazy does)
		if unlazier, ok := remote.Provider.(cache.Unlazier); ok {
			if err := unlazier.Unlazy(ctx); err != nil {
				return nil, err
			}
		}
		for _, desc := range remote.Descriptors {
			mprovider.Add(desc.Digest, remote.Provider)
		}
		mproviders = append(mproviders, mprovider)
	}

	report := oneOffProgress(ctx, "sending tarballs")
	for i := 0; i < numImages; i++ {
		w := writers[i]
		desc := descs[i]
		expOpts := []archiveexporter.ExportOpt{archiveexporter.WithManifest(*desc, names[i]...)}
		switch e.opt.Variant {
		case VariantOCI:
			expOpts = append(expOpts, archiveexporter.WithAllPlatforms(), archiveexporter.WithSkipDockerManifest())
		case VariantDocker:
		default:
			return nil, report(errors.Errorf("invalid variant %q", e.opt.Variant))
		}
		if err := archiveexporter.Export(ctx, mproviders[i], w, expOpts...); err != nil {
			w.Close()
			if grpcerrors.Code(err) == codes.AlreadyExists {
				continue
			}
			return nil, report(err)
		}
		err = w.Close()
		if grpcerrors.Code(err) == codes.AlreadyExists {
			continue
		}
		if err != nil {
			return nil, report(err)
		}
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return resp, report(nil)
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

func newProgressHandler(ctx context.Context, id string) func(int, bool) {
	limiter := rate.NewLimiter(rate.Every(100*time.Millisecond), 1)
	pw, _, _ := progress.FromContext(ctx)
	now := time.Now()
	st := progress.Status{
		Started: &now,
		Action:  "transferring",
	}
	pw.Write(id, st)
	return func(s int, last bool) {
		if last || limiter.Allow() {
			st.Current = s
			if last {
				now := time.Now()
				st.Completed = &now
			}
			pw.Write(id, st)
			if last {
				pw.Close()
			}
		}
	}
}

func exportDirFunc(ctx context.Context, md map[string]string, caller session.Caller, ref cache.ImmutableRef) func() error {
	return func() error {
		var src string
		var err error
		var idmap *idtools.IdentityMapping
		if ref == nil {
			src, err = ioutil.TempDir("", "buildkit")
			if err != nil {
				return err
			}
			defer os.RemoveAll(src)
		} else {
			mount, err := ref.Mount(ctx, true)
			if err != nil {
				return err
			}

			lm := snapshot.LocalMounter(mount)

			src, err = lm.Mount()
			if err != nil {
				return err
			}

			idmap = mount.IdentityMapping()

			defer lm.Unmount()
		}

		walkOpt := &fsutil.WalkOpt{}

		if idmap != nil {
			walkOpt.Map = func(p string, st *fstypes.Stat) bool {
				uid, gid, err := idmap.ToContainer(idtools.Identity{
					UID: int(st.Uid),
					GID: int(st.Gid),
				})
				if err != nil {
					return false
				}
				st.Uid = uint32(uid)
				st.Gid = uint32(gid)
				return true
			}
		}

		fs := fsutil.NewFS(src, walkOpt)
		progress := newProgressHandler(ctx, "copying files")
		if err := filesync.CopyToCallerWithMeta(ctx, md, fs, caller, progress); err != nil {
			return err
		}
		return nil
	}
}
