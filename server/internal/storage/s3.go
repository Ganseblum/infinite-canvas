package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	signer "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const s3ServiceName = "s3"

// S3Config 是 S3 兼容驱动的配置，全部来自环境变量。
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
	// PresignAlign 是签发时间的对齐粒度；PresignTTL 是有效期，必须等于 2 × Align。
	PresignAlign time.Duration
	PresignTTL   time.Duration
}

// S3 是 S3 兼容对象存储驱动。写路径经后端代理，读路径签出对齐整点的预签名 URL。
type S3 struct {
	cfg    S3Config
	client *s3.Client
	signer *signer.Signer
	creds  aws.Credentials
	// now 可注入，测试里用来断言同一对齐窗口内签出的 URL 完全一致。
	now func() time.Time
}

func NewS3(cfg S3Config) (*S3, error) {
	if cfg.PresignAlign <= 0 || cfg.PresignTTL != 2*cfg.PresignAlign {
		return nil, fmt.Errorf("S3 预签名配置非法: TTL(%s) 必须等于 2 × ALIGN(%s)", cfg.PresignTTL, cfg.PresignAlign)
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("S3_ENDPOINT 非法: %s", cfg.Endpoint)
	}
	static := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: aws.NewCredentialsCache(static),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
	})
	creds, err := static.Retrieve(context.Background())
	if err != nil {
		return nil, err
	}
	return &S3{
		cfg:    cfg,
		client: client,
		signer: signer.NewSigner(),
		creds:  creds,
		now:    time.Now,
	}, nil
}

func (s *S3) Kind() string { return "s3" }

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (s *S3) Put(ctx context.Context, path string, r io.Reader, contentType string) (int64, string, error) {
	h := sha256.New()
	cr := &countingReader{r: io.TeeReader(r, h)}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(path),
		Body:        cr,
		ContentType: aws.String(contentType),
		// 媒体读取走带鉴权的 API 响应头，对象元数据只服务 302 预签名直读场景的缓存兜底。
		// 同一 key 可被覆盖上传（PUT 覆盖语义），不能 immutable，保守缓存一天。
		CacheControl: aws.String("private, max-age=86400"),
	})
	if err != nil {
		// 返回已读字节数：handler 据此判断是否为超过单文件上限的中断。
		return cr.n, "", err
	}
	return cr.n, hex.EncodeToString(h.Sum(nil)), nil
}

func (s *S3) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		return nil, mapS3NotFound(err)
	}
	return out.Body, nil
}

func (s *S3) Delete(ctx context.Context, path string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(path),
	})
	return err
}

func (s *S3) Stat(ctx context.Context, path string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(path),
	})
	if err != nil {
		return 0, mapS3NotFound(err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

// Presign 生成对齐整点的预签名 URL：签发时间截断到 PresignAlign，有效期 PresignTTL。
// 同一对齐窗口内的请求拿到完全相同的 URL，浏览器与 CDN 才能共用缓存键。
// 这里显式把对齐后的时间传给 SDK 的 SigV4 签名器，签名本身仍由 SDK 计算。
func (s *S3) Presign(ctx context.Context, path string) (Presigned, error) {
	signedAt := s.now().UTC().Truncate(s.cfg.PresignAlign)
	objectURL, err := s.objectURL(path)
	if err != nil {
		return Presigned{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURL, nil)
	if err != nil {
		return Presigned{}, err
	}
	q := req.URL.Query()
	q.Set("X-Amz-Expires", strconv.FormatInt(int64(s.cfg.PresignTTL.Seconds()), 10))
	req.URL.RawQuery = q.Encode()

	uri, _, err := s.signer.PresignHTTP(ctx, s.creds, req, "UNSIGNED-PAYLOAD", s3ServiceName, s.cfg.Region, signedAt)
	if err != nil {
		return Presigned{}, err
	}
	return Presigned{URL: uri, ExpiresAt: signedAt.Add(s.cfg.PresignTTL)}, nil
}

// objectURL 按 S3_FORCE_PATH_STYLE 组装对象地址；bucket 与对象路径都由服务端控制，
// 不含用户可编辑的任意字符。
func (s *S3) objectURL(path string) (string, error) {
	endpoint, err := url.Parse(s.cfg.Endpoint)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimSuffix(endpoint.Path, "/")
	u := &url.URL{Scheme: endpoint.Scheme, Host: endpoint.Host}
	if s.cfg.ForcePathStyle {
		u.Path = basePath + "/" + s.cfg.Bucket + "/" + path
	} else {
		u.Host = s.cfg.Bucket + "." + endpoint.Host
		u.Path = basePath + "/" + path
	}
	return u.String(), nil
}

// mapS3NotFound 把 S3 的对象不存在错误统一映射为 ErrObjectNotFound。
func mapS3NotFound(err error) error {
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return ErrObjectNotFound
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return ErrObjectNotFound
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchKey" {
		return ErrObjectNotFound
	}
	return err
}
