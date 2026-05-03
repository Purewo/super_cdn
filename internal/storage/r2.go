package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	r2MultipartThreshold = int64(128 << 20)
	r2MultipartPartSize  = int64(64 << 20)
	r2MultipartMaxParts  = int64(10000)
)

type R2Store struct {
	name      string
	bucket    string
	publicURL string
	client    *s3.Client
}

type R2Options struct {
	Name            string
	AccountID       string
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	PublicBaseURL   string
	ProxyURL        string
}

func NewR2Store(ctx context.Context, opts R2Options) (*R2Store, error) {
	endpoint := opts.Endpoint
	if endpoint == "" && opts.AccountID != "" {
		endpoint = "https://" + opts.AccountID + ".r2.cloudflarestorage.com"
	}
	httpClient, err := newHTTPClient(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("auto"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, "")),
		config.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = true
	})
	return &R2Store{name: opts.Name, bucket: opts.Bucket, publicURL: strings.TrimRight(opts.PublicBaseURL, "/"), client: client}, nil
}

func (s *R2Store) Name() string { return s.name }
func (s *R2Store) Type() string { return "r2" }
func (s *R2Store) Capabilities() Capabilities {
	return Capabilities{
		CanUpload:                true,
		CanDeleteRemote:          true,
		CanProducePublicLocator:  strings.TrimSpace(s.publicURL) != "",
		SupportsRangeGET:         true,
		SupportsHEAD:             true,
		HTTPOnlyLocatorRisk:      strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.publicURL)), "http://"),
		WebResourceSuitability:   "legacy_compatibility",
		CDNBucketSuitability:     "preferred_overseas",
		ImmutableCIDBehavior:     false,
		PreferredCachePolicy:     "public, max-age=31536000, immutable for CDN objects; caller-defined for Web compatibility",
		DirectLocatorDescription: "R2 public custom domain or r2.dev URL",
		Notes:                    []string{"R2 is maintained for CDN/object acceleration; new mainstream Web hosting should prefer Cloudflare entry plus non-R2 resource libraries"},
	}
}

func (s *R2Store) Put(ctx context.Context, opts PutOptions) (string, error) {
	f, err := os.Open(opts.FilePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	size := opts.Size
	if size <= 0 {
		if info, err := f.Stat(); err == nil {
			size = info.Size()
		}
	}
	if r2UseMultipart(size) {
		return s.putMultipart(ctx, f, opts, size)
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(opts.Key),
		Body:         f,
		ContentType:  aws.String(firstNonEmpty(opts.ContentType, detectByName(opts.Key))),
		CacheControl: emptyAsNil(opts.CacheControl),
		Metadata: map[string]string{
			"sha256": opts.SHA256,
		},
	})
	if err != nil {
		return "", err
	}
	return s.PublicURL(opts.Key), nil
}

func (s *R2Store) putMultipart(ctx context.Context, f *os.File, opts PutOptions, size int64) (string, error) {
	create, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(opts.Key),
		ContentType:  aws.String(firstNonEmpty(opts.ContentType, detectByName(opts.Key))),
		CacheControl: emptyAsNil(opts.CacheControl),
		Metadata: map[string]string{
			"sha256": opts.SHA256,
		},
	})
	if err != nil {
		return "", err
	}
	uploadID := aws.ToString(create.UploadId)
	partSize, partCount := r2MultipartPlan(size)
	completed := make([]types.CompletedPart, 0, partCount)
	for partNumber, offset := int32(1), int64(0); offset < size; partNumber, offset = partNumber+1, offset+partSize {
		currentSize := partSize
		if remaining := size - offset; remaining < currentSize {
			currentSize = remaining
		}
		out, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:        aws.String(s.bucket),
			Key:           aws.String(opts.Key),
			UploadId:      aws.String(uploadID),
			PartNumber:    aws.Int32(partNumber),
			ContentLength: aws.Int64(currentSize),
			Body:          io.NewSectionReader(f, offset, currentSize),
		})
		if err != nil {
			_, _ = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.bucket),
				Key:      aws.String(opts.Key),
				UploadId: aws.String(uploadID),
			})
			return "", err
		}
		completed = append(completed, types.CompletedPart{ETag: out.ETag, PartNumber: aws.Int32(partNumber)})
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(opts.Key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		_, _ = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.bucket),
			Key:      aws.String(opts.Key),
			UploadId: aws.String(uploadID),
		})
		return "", err
	}
	return s.PublicURL(opts.Key), nil
}

func (s *R2Store) Get(ctx context.Context, key string, opts GetOptions) (*ObjectStream, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if opts.Range != "" {
		input.Range = aws.String(opts.Range)
	}
	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	status := http.StatusOK
	if out.ContentRange != nil {
		status = http.StatusPartialContent
	}
	return &ObjectStream{
		Body:         out.Body,
		StatusCode:   status,
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		CacheControl: aws.ToString(out.CacheControl),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		ContentRange: aws.ToString(out.ContentRange),
		Locator:      s.PublicURL(key),
	}, nil
}

func (s *R2Store) Stat(ctx context.Context, key string) (*Stat, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Stat{
		Size:         aws.ToInt64(out.ContentLength),
		ContentType:  aws.ToString(out.ContentType),
		CacheControl: aws.ToString(out.CacheControl),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		Locator:      s.PublicURL(key),
	}, nil
}

func (s *R2Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *R2Store) PublicURL(key string) string {
	if s.publicURL == "" {
		return ""
	}
	return s.publicURL + "/" + strings.TrimLeft(key, "/")
}

func (s *R2Store) HealthCheck(ctx context.Context, opts HealthCheckOptions) (*HealthCheckResult, error) {
	checkedAt := time.Now().UTC()
	mode := HealthModePassive
	if opts.WriteProbe {
		mode = HealthModeWriteProbe
	}
	item := HealthCheckItem{
		Target:     s.name,
		TargetType: s.Type(),
		Status:     HealthStatusOK,
		CheckMode:  mode,
		CheckedAt:  checkedAt,
	}
	prefix := normalizePrefix(opts.Prefix)
	start := time.Now()
	_, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		Prefix:  emptyAsNil(prefix),
		MaxKeys: aws.Int32(1),
	})
	item.ListLatencyMS = elapsedMS(start)
	if err != nil {
		item.Status = HealthStatusFailed
		item.LastError = err.Error()
		return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
	}
	if opts.WriteProbe {
		if err := s.r2WriteProbe(ctx, opts, &item); err != nil {
			item.Status = HealthStatusFailed
			item.LastError = err.Error()
			return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, err
		}
	}
	return &HealthCheckResult{Target: s.name, TargetType: s.Type(), Items: []HealthCheckItem{item}}, nil
}

func (s *R2Store) InitDirs(ctx context.Context, opts InitOptions) (*InitResult, error) {
	result := &InitResult{Target: s.name, TargetType: s.Type()}
	dirs, err := expandInitDirs(opts.Directories)
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		item := InitPathResult{Path: dir, RemotePath: strings.Trim(dir, "/"), Status: "virtual"}
		if opts.DryRun {
			item.Status = "planned"
		}
		result.Directories = append(result.Directories, item)
	}
	if opts.MarkerPath == "" {
		return result, nil
	}
	marker := strings.Trim(strings.ReplaceAll(opts.MarkerPath, "\\", "/"), "/")
	item := InitPathResult{Path: opts.MarkerPath, RemotePath: marker}
	if opts.DryRun {
		item.Status = "planned"
		result.Files = append(result.Files, item)
		return result, nil
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(marker),
		Body:        bytes.NewReader(opts.MarkerPayload),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		item.Status = "error"
		item.Error = err.Error()
		result.Files = append(result.Files, item)
		return result, err
	}
	item.Status = "written"
	result.Files = append(result.Files, item)
	return result, nil
}

func (s *R2Store) r2WriteProbe(ctx context.Context, opts HealthCheckOptions, item *HealthCheckItem) error {
	key := firstNonEmpty(opts.ProbeKey, joinStorePrefix(opts.Prefix, "_supercdn/healthcheck.tmp"))
	payload := opts.ProbePayload
	if len(payload) == 0 {
		payload = []byte("supercdn health probe\n")
	}
	start := time.Now()
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(payload),
		ContentType: aws.String("text/plain; charset=utf-8"),
	})
	item.WriteLatencyMS = elapsedMS(start)
	if err != nil {
		return err
	}
	defer func() {
		start := time.Now()
		_, _ = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		item.DeleteLatencyMS = elapsedMS(start)
	}()
	start = time.Now()
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	item.ReadLatencyMS = elapsedMS(start)
	if err != nil {
		return err
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(out.Body, int64(len(payload))+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, payload) {
		return errors.New("r2 health probe readback mismatch")
	}
	return nil
}

func r2UseMultipart(size int64) bool {
	return size > r2MultipartThreshold
}

func r2MultipartPlan(size int64) (int64, int) {
	if size <= 0 {
		return r2MultipartPartSize, 0
	}
	partSize := r2MultipartPartSize
	if parts := (size + partSize - 1) / partSize; parts > r2MultipartMaxParts {
		partSize = (size + r2MultipartMaxParts - 1) / r2MultipartMaxParts
	}
	parts := int((size + partSize - 1) / partSize)
	return partSize, parts
}

func emptyAsNil(v string) *string {
	if v == "" {
		return nil
	}
	return aws.String(v)
}

func isS3NotFound(err error) bool {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *types.NotFound
	return errors.As(err, &notFound)
}
