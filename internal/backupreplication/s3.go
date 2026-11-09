// Package backupreplication copies opaque published bundles; it has no Tessera
// administrator, producer, record, recovery-key or kit authority.
package backupreplication

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
)

var ErrUnavailable = errors.New("remote copy unavailable; retain pending inputs")
var ErrConflict = errors.New("immutable remote object conflicts")
var ErrMissing = errors.New("remote object missing")
var ErrExists = errors.New("remote object already exists")
var hexID = regexp.MustCompile("^[0-9a-f]{64}$")
var prefixPattern = regexp.MustCompile("^[a-z0-9][a-z0-9/-]{0,95}/$")
var regionPattern = regexp.MustCompile("^[a-z]{2}(-[a-z]+)+-[0-9]+$")
var bucketPattern = regexp.MustCompile("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$")

type Config struct {
	ExpectedBucketOwner                       string
	Version                                   int
	Label, Region, Bucket, Prefix, Deployment string
	CredentialFile, Endpoint, CAFile          string
	Outbox, State, StatusFile, SettingsFile   string
	Budget                                    int64
	LocalFixture, IndependentDestination      bool
}
type credential struct{ AccessKeyID, SecretAccessKey, SessionToken string }
type Objects interface {
	Get(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, io.ReadSeeker, int64) error
	List(context.Context, string) ([]string, error)
}

func (c Config) validate() error {
	if c.Version != 1 || !bc.SafeLabel(c.Label) || !regionPattern.MatchString(c.Region) || !bucketPattern.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") || strings.HasSuffix(c.Bucket, "--x-s3") || !prefixPattern.MatchString(c.Prefix) || strings.Contains(c.Prefix, "//") || len(c.Deployment) != 32 {
		return ErrUnavailable
	}
	for _, v := range c.Deployment {
		if !strings.ContainsRune("0123456789abcdef", v) {
			return ErrUnavailable
		}
	}
	if !c.LocalFixture && !regexp.MustCompile("^[0-9]{12}$").MatchString(c.ExpectedBucketOwner) {
		return ErrUnavailable
	}
	if c.LocalFixture {
		u, e := url.Parse(c.Endpoint)
		if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || c.CAFile == "" {
			return ErrUnavailable
		}
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return ErrUnavailable
		}
	} else if c.Endpoint != "" || c.CAFile != "" {
		return ErrUnavailable
	}
	return nil
}
func NewS3(c Config) (Objects, error) {
	if c.validate() != nil {
		return nil, ErrUnavailable
	}
	var key credential
	if bc.ReadPrivateJSON(c.CredentialFile, &key) != nil || key.AccessKeyID == "" || key.SecretAccessKey == "" {
		return nil, ErrUnavailable
	}
	st, e := os.Lstat(c.CredentialFile)
	if e != nil || st.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		return nil, ErrUnavailable
	}
	if c.LocalFixture {
		raw, e := os.ReadFile(c.CAFile)
		if e != nil || !roots.AppendCertsFromPEM(raw) {
			return nil, ErrUnavailable
		}
	}
	client := &http.Client{Timeout: 110 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// No LoadDefaultConfig, environment/profile/metadata provider or cloud discovery.
	cfg := aws.Config{Region: c.Region, Credentials: credentials.NewStaticCredentialsProvider(key.AccessKeyID, key.SecretAccessKey, key.SessionToken), HTTPClient: client, RetryMaxAttempts: 2, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}
	sdk := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if c.LocalFixture {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
	})
	var owner *string
	if c.ExpectedBucketOwner != "" {
		owner = aws.String(c.ExpectedBucketOwner)
	}
	return &s3Objects{sdk: sdk, bucket: c.Bucket, owner: owner}, nil
}

type s3Objects struct {
	owner  *string
	sdk    *s3.Client
	bucket string
}

func remoteError(e error) error {
	if e == nil {
		return nil
	}
	var code interface{ ErrorCode() string }
	if errors.As(e, &code) {
		switch code.ErrorCode() {
		case "PreconditionFailed":
			return ErrExists
		case "NoSuchKey", "NotFound":
			return ErrMissing
		}
	}
	return ErrUnavailable
}
func (s *s3Objects) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	v, e := s.sdk.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), ExpectedBucketOwner: s.owner, Key: aws.String(key)})
	if e != nil {
		return nil, remoteError(e)
	}
	return v.Body, nil
}
func (s *s3Objects) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	_, e := s.sdk.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), ExpectedBucketOwner: s.owner, Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), IfNoneMatch: aws.String("*"), StorageClass: "STANDARD"})
	return remoteError(e)
}
func (s *s3Objects) List(ctx context.Context, prefix string) (out []string, e error) {
	var next *string
	seen := map[string]bool{}
	for page := 0; page < 24; page++ {
		v, err := s.sdk.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), ExpectedBucketOwner: s.owner, Prefix: aws.String(prefix), ContinuationToken: next, MaxKeys: aws.Int32(1000)})
		if err != nil {
			return nil, remoteError(err)
		}
		for _, item := range v.Contents {
			key := aws.ToString(item.Key)
			if !strings.HasPrefix(key, prefix) || seen[key] {
				return nil, ErrUnavailable
			}
			seen[key] = true
			out = append(out, key)
			if len(out) > 10000 {
				return nil, ErrUnavailable
			}
		}
		if !aws.ToBool(v.IsTruncated) {
			return out, nil
		}
		if v.NextContinuationToken == nil || aws.ToString(v.NextContinuationToken) == aws.ToString(next) {
			return nil, ErrUnavailable
		}
		next = v.NextContinuationToken
	}
	return nil, ErrUnavailable
}
