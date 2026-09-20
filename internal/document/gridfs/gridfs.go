// Package gridfs implements document.Storage on MongoDB GridFS, so files live next to the rest of
// the data with nothing else to run. Because there is no external store to sign URLs for, the API
// signs its own short-lived links and streams the bytes itself, keeping the same
// upload/download contract clients already use.
package gridfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/document"
	"rinotravel-api/internal/platform/httpx"
)

const (
	// transferTimeout extends the server-wide read/write deadlines for one file transfer, which on
	// a slow mobile connection can take far longer than a normal API call.
	transferTimeout = 10 * time.Minute
	minSecretLength = 32
	linkPath        = "/api/v1/storage/"
)

type Config struct {
	// Secret signs the links; keep it long and private.
	Secret string
	// PublicURL is where clients reach this API (for example https://api.example.com).
	PublicURL string
}

type Storage struct {
	bucket    *mongo.GridFSBucket
	secret    []byte
	publicURL string
	logger    *slog.Logger
	now       func() time.Time
}

func New(db *mongo.Database, cfg Config, logger *slog.Logger) (*Storage, error) {
	if len(cfg.Secret) < minSecretLength {
		return nil, fmt.Errorf("storage signing secret must have at least %d characters", minSecretLength)
	}
	return &Storage{
		bucket:    db.GridFSBucket(),
		secret:    []byte(cfg.Secret),
		publicURL: strings.TrimRight(cfg.PublicURL, "/"),
		logger:    logger,
		now:       time.Now,
	}, nil
}

func (s *Storage) link(c claims, ttl time.Duration) document.Upload {
	c.Expires = s.now().Add(ttl).Unix()
	return document.Upload{
		URL:       s.publicURL + linkPath + sign(s.secret, c),
		ExpiresAt: time.Unix(c.Expires, 0).UTC(),
	}
}

func (s *Storage) PresignUpload(_ context.Context, key string, req document.UploadRequest) (document.Upload, error) {
	up := s.link(claims{Op: opPut, Key: key, Size: req.Size, SHA256: req.ChecksumSHA256, ContentType: req.ContentType}, req.TTL)
	up.Method = http.MethodPut
	up.Headers = map[string]string{"Content-Type": req.ContentType}
	return up, nil
}

func (s *Storage) PresignDownload(_ context.Context, key string, req document.DownloadRequest) (document.Upload, error) {
	down := s.link(claims{Op: opGet, Key: key, ContentType: req.ContentType, FileName: req.FileName}, req.TTL)
	down.Method = http.MethodGet
	return down, nil
}

type fileDoc struct {
	Length   int64 `bson:"length"`
	Metadata struct {
		SHA256      string `bson:"sha256"`
		ContentType string `bson:"contentType"`
	} `bson:"metadata"`
}

func (s *Storage) Stat(ctx context.Context, key string) (document.ObjectInfo, error) {
	var f fileDoc
	err := s.bucket.GetFilesCollection().FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&f)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return document.ObjectInfo{}, document.ErrObjectNotFound
	}
	if err != nil {
		return document.ObjectInfo{}, err
	}
	return document.ObjectInfo{Size: f.Length, ContentType: f.Metadata.ContentType, ChecksumSHA256: f.Metadata.SHA256}, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := s.bucket.Delete(ctx, key); err != nil && !errors.Is(err, mongo.ErrFileNotFound) {
		return err
	}
	return nil
}

// Mount serves the signed links. They carry their own authorization, so no bearer token is needed.
func (s *Storage) Mount(mux *http.ServeMux) {
	mux.Handle("PUT "+linkPath+"{token}", httpx.Handle(s.logger, s.upload))
	mux.Handle("GET "+linkPath+"{token}", httpx.Handle(s.logger, s.download))
}

func (s *Storage) claims(r *http.Request, op string) (claims, error) {
	c, err := verify(s.secret, r.PathValue("token"), s.now())
	switch {
	case errors.Is(err, errExpiredToken):
		return claims{}, apperror.Forbidden("link_expired", "This link has expired. Request a new one.")
	case err != nil || c.Op != op:
		return claims{}, apperror.Forbidden("invalid_link", "This link is not valid.")
	}
	return c, nil
}

func extendDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(transferTimeout)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}

func (s *Storage) upload(w http.ResponseWriter, r *http.Request) error {
	c, err := s.claims(r, opPut)
	if err != nil {
		return err
	}
	if r.ContentLength != c.Size {
		return apperror.BadRequest("size_mismatch", "The request must carry exactly the declared number of bytes.")
	}
	extendDeadlines(w)

	// A retry after a failed attempt replaces whatever was left behind.
	if err := s.Delete(r.Context(), c.Key); err != nil {
		return fmt.Errorf("clear previous object: %w", err)
	}

	hasher := sha256.New()
	body := io.TeeReader(http.MaxBytesReader(w, r.Body, c.Size), hasher)
	metadata := bson.D{{Key: "sha256", Value: c.SHA256}, {Key: "contentType", Value: c.ContentType}}
	if err := s.bucket.UploadFromStreamWithID(r.Context(), c.Key, c.Key, body, options.GridFSUpload().SetMetadata(metadata)); err != nil {
		_ = s.Delete(context.WithoutCancel(r.Context()), c.Key)
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return apperror.BadRequest("size_mismatch", "The request carried more bytes than declared.")
		}
		return fmt.Errorf("upload object: %w", err)
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != c.SHA256 {
		_ = s.Delete(context.WithoutCancel(r.Context()), c.Key)
		return apperror.Unprocessable("upload_mismatch", "The uploaded content does not match the declared checksum.")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Storage) download(w http.ResponseWriter, r *http.Request) error {
	c, err := s.claims(r, opGet)
	if err != nil {
		return err
	}
	stream, err := s.bucket.OpenDownloadStream(r.Context(), c.Key)
	if errors.Is(err, mongo.ErrFileNotFound) {
		return apperror.NotFound("object_not_found", "The file no longer exists.")
	}
	if err != nil {
		return fmt.Errorf("open object: %w", err)
	}
	defer stream.Close()
	extendDeadlines(w)

	h := w.Header()
	h.Set("Content-Type", c.ContentType)
	h.Set("Content-Disposition", document.ContentDisposition(c.FileName))
	h.Set("Content-Length", fmt.Sprint(stream.GetFile().Length))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	if _, err := io.Copy(w, stream); err != nil {
		s.logger.WarnContext(r.Context(), "stream object", slog.Any("error", err))
	}
	return nil
}
