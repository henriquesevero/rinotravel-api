// Package documenttest is an in-memory document.Storage for tests.
package documenttest

import (
	"context"
	"sync"
	"time"

	"rinotravel-api/internal/document"
)

type object struct {
	info document.ObjectInfo
}

type Storage struct {
	mu      sync.Mutex
	objects map[string]object
	Deleted []string
}

func NewStorage() *Storage { return &Storage{objects: map[string]object{}} }

// Put simulates the client's upload landing in the store.
func (s *Storage) Put(key string, size int64, checksum string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = object{info: document.ObjectInfo{Size: size, ChecksumSHA256: checksum}}
}

func (s *Storage) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

func (s *Storage) PresignUpload(_ context.Context, key string, req document.UploadRequest) (document.Upload, error) {
	return document.Upload{
		URL: "https://storage.test/upload/" + key, Method: "PUT",
		Headers:   map[string]string{"Content-Type": req.ContentType, "X-Amz-Checksum-Sha256": req.ChecksumSHA256},
		ExpiresAt: time.Now().Add(req.TTL),
	}, nil
}

func (s *Storage) Stat(_ context.Context, key string) (document.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	if !ok {
		return document.ObjectInfo{}, document.ErrObjectNotFound
	}
	return o.info, nil
}

func (s *Storage) PresignDownload(_ context.Context, key string, req document.DownloadRequest) (document.Upload, error) {
	return document.Upload{URL: "https://storage.test/download/" + key + "?name=" + req.FileName, Method: "GET", ExpiresAt: time.Now().Add(req.TTL)}, nil
}

func (s *Storage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	s.Deleted = append(s.Deleted, key)
	return nil
}
