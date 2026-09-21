package document

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type InitInput struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	FileName   string     `json:"fileName"`
	MimeType   string     `json:"mimeType"`
	Size       int64      `json:"size"`
	Checksum   string     `json:"checksum"`
	Visibility *string    `json:"visibility"`
	Link       *LinkInput `json:"link"`
}

// Patch edits metadata only: the file itself (size, type, checksum) is immutable.
type Patch struct {
	kernel.Versioned
	Name       *string                    `json:"name"`
	Type       *string                    `json:"type"`
	Visibility *string                    `json:"visibility"`
	Link       kernel.Optional[LinkInput] `json:"link"`
}

type Documents struct {
	res     *resource.Service[Document]
	authz   resource.Authorizer
	storage Storage
	// keyPrefix namespaces objects per deployment, for example "production".
	keyPrefix string
	logger    *slog.Logger
}

func NewDocuments(repo resource.Repo[Document], authz resource.Authorizer, storage Storage, keyPrefix string, logger *slog.Logger) *Documents {
	return &Documents{
		res:       resource.NewService[Document](repo, authz, resource.Config[Document]{Name: "document", Base: Base}),
		authz:     authz,
		storage:   storage,
		keyPrefix: keyPrefix,
		logger:    logger,
	}
}

func (d *Documents) Resource() *resource.Service[Document] { return d.res }

func notFound() *apperror.Error {
	return apperror.NotFound("document_not_found", "The requested resource does not exist.")
}

type Initiated struct {
	Document Document
	Upload   Upload
}

// Init records the document as PENDING and returns the signed request for the client to upload
// the file with. The document stays invisible to everyone else until Complete confirms the file.
func (d *Documents) Init(ctx context.Context, actor user.ID, tripID trip.ID, in InitInput) (Initiated, error) {
	if _, err := d.authz.Authorize(ctx, tripID, actor, trip.ActionWriteContent); err != nil {
		return Initiated{}, err
	}

	var v kernel.Validator
	name := v.Text("name", in.Name, true, 200)
	docType, err := ParseType(in.Type)
	if err != nil {
		v.Add("type", err.Error())
	}
	fileName := SanitizeFileName(in.FileName)
	v.Check(fileName != "", "fileName", "is required")
	v.Check(allowedMime[in.MimeType], "mimeType", "must be a PDF, JPEG, PNG, WebP or HEIC file")
	v.Check(in.Size > 0 && in.Size <= MaxSize, "size", "must be between 1 byte and 25 MB")
	v.Check(checksumPattern.MatchString(in.Checksum), "checksum", "must be the SHA-256 of the file as 64 lowercase hex characters")
	visibility := VisibilityTrip
	if docType == TypePassport {
		visibility = VisibilityPrivate
	}
	if in.Visibility != nil {
		if visibility, err = ParseVisibility(*in.Visibility); err != nil {
			v.Add("visibility", err.Error())
		}
	}
	linkType, linkID := linkValidator{&v}.link("link", in.Link)
	if err := v.Err(); err != nil {
		return Initiated{}, err
	}

	id := in.ID
	if id == "" {
		id = ids.New()
	}
	key := fmt.Sprintf("%s/trips/%s/docs/%s", d.keyPrefix, tripID, id)
	upload, err := d.storage.PresignUpload(ctx, key, UploadRequest{
		ContentType: in.MimeType, Size: in.Size, ChecksumSHA256: in.Checksum, TTL: UploadTTL,
	})
	if err != nil {
		return Initiated{}, fmt.Errorf("presign upload: %w", err)
	}

	res, err := d.res.Create(ctx, actor, tripID, id, func(trip.Access) (Document, error) {
		return Document{
			OwnerID: actor, Name: name, Type: docType, FileName: fileName, MimeType: in.MimeType, Size: in.Size,
			StorageKey: key, Checksum: in.Checksum, Status: StatusPending, Visibility: visibility,
			LinkType: linkType, LinkID: linkID,
		}, nil
	})
	if err != nil {
		return Initiated{}, err
	}
	return Initiated{Document: res.Entity, Upload: upload}, nil
}

// Complete verifies the uploaded object against what Init promised and only then publishes the
// document. Calling it again on a ready document is a no-op.
func (d *Documents) Complete(ctx context.Context, actor user.ID, tripID trip.ID, id string) (resource.Result[Document], error) {
	cur, err := d.res.Get(ctx, actor, tripID, id)
	if err != nil {
		return resource.Result[Document]{}, err
	}
	doc := cur.Entity
	if doc.OwnerID != actor || !doc.VisibleTo(actor) {
		return resource.Result[Document]{}, notFound()
	}
	if doc.Status == StatusReady {
		return cur, nil
	}

	info, err := d.storage.Stat(ctx, doc.StorageKey)
	if errors.Is(err, ErrObjectNotFound) {
		return resource.Result[Document]{}, apperror.Unprocessable("upload_missing", "The file has not been uploaded yet.")
	}
	if err != nil {
		return resource.Result[Document]{}, fmt.Errorf("stat object: %w", err)
	}
	if info.Size != doc.Size || (info.ChecksumSHA256 != "" && info.ChecksumSHA256 != doc.Checksum) {
		_ = d.storage.Delete(ctx, doc.StorageKey)
		return resource.Result[Document]{}, apperror.Unprocessable("upload_mismatch", "The uploaded file does not match the size or checksum that was declared. Upload it again.")
	}

	return d.res.Update(ctx, actor, tripID, id, doc.Version, func(current Document, _ trip.Access) (Document, error) {
		current.Status = StatusReady
		return current, nil
	})
}

func (d *Documents) Get(ctx context.Context, actor user.ID, tripID trip.ID, id string) (resource.Result[Document], error) {
	res, err := d.res.Get(ctx, actor, tripID, id)
	if err != nil {
		return resource.Result[Document]{}, err
	}
	if !readable(res.Entity, actor) {
		return resource.Result[Document]{}, notFound()
	}
	return res, nil
}

// Readable reports whether the actor can see a finished document of the trip. Other features use it
// to attach a document to their own records without knowing how documents are stored.
func (d *Documents) Readable(ctx context.Context, actor user.ID, tripID trip.ID, id string) (bool, error) {
	res, err := d.Get(ctx, actor, tripID, id)
	var app *apperror.Error
	switch {
	case errors.As(err, &app) && app.Kind == apperror.KindNotFound:
		return false, nil
	case err != nil:
		return false, err
	}
	return res.Entity.Status == StatusReady, nil
}

func (d *Documents) List(ctx context.Context, actor user.ID, tripID trip.ID) ([]Document, trip.Role, error) {
	all, role, err := d.res.List(ctx, actor, tripID)
	if err != nil {
		return nil, "", err
	}
	visible := make([]Document, 0, len(all))
	for _, doc := range all {
		if readable(doc, actor) {
			visible = append(visible, doc)
		}
	}
	return visible, role, nil
}

// readable hides other people's private documents and files that are not confirmed yet (which
// only their uploader can see).
func readable(doc Document, actor user.ID) bool {
	if !doc.VisibleTo(actor) {
		return false
	}
	return doc.Status == StatusReady || doc.OwnerID == actor
}

func canManage(doc Document, actor user.ID, role trip.Role) bool {
	return doc.OwnerID == actor || role == trip.RoleOwner || role == trip.RoleAdmin
}

func (d *Documents) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p Patch) (resource.Result[Document], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Document]{}, err
	}
	return d.res.Update(ctx, actor, tripID, id, version, func(cur Document, access trip.Access) (Document, error) {
		if !readable(cur, actor) {
			return Document{}, notFound()
		}
		if !canManage(cur, actor, access.Role) {
			return Document{}, apperror.Forbidden("forbidden", "Only the uploader or a trip admin can change this document.")
		}
		var v kernel.Validator
		if p.Name != nil {
			cur.Name = v.Text("name", *p.Name, true, 200)
		}
		if p.Type != nil {
			t, err := ParseType(*p.Type)
			if err != nil {
				v.Add("type", err.Error())
			}
			cur.Type = t
		}
		if p.Visibility != nil {
			vis, err := ParseVisibility(*p.Visibility)
			if err != nil {
				v.Add("visibility", err.Error())
			}
			cur.Visibility = vis
		}
		if p.Link.Set {
			cur.LinkType, cur.LinkID = "", ""
			if !p.Link.Clear {
				cur.LinkType, cur.LinkID = linkValidator{&v}.link("link", &p.Link.Value)
			}
		}
		return cur, v.Err()
	})
}

func (d *Documents) Delete(ctx context.Context, actor user.ID, tripID trip.ID, id string, baseVersion *int64) error {
	cur, err := d.res.Get(ctx, actor, tripID, id)
	if err != nil {
		return err
	}
	if !readable(cur.Entity, actor) {
		return notFound()
	}
	if !canManage(cur.Entity, actor, cur.Role) {
		return apperror.Forbidden("forbidden", "Only the uploader or a trip admin can delete this document.")
	}
	if err := d.res.Delete(ctx, actor, tripID, id, baseVersion); err != nil {
		return err
	}
	// The metadata is already a tombstone; a failure here only leaves an orphan object for cleanup.
	if err := d.storage.Delete(ctx, cur.Entity.StorageKey); err != nil {
		d.logger.WarnContext(ctx, "delete document object", slog.String("document", id), slog.Any("error", err))
	}
	return nil
}

type Download struct {
	Document Document
	Request  Upload
}

// Download authorizes the caller and signs a short-lived URL. Authorization is checked at signing
// time only, which is why the URL lives just a few minutes.
func (d *Documents) Download(ctx context.Context, actor user.ID, tripID trip.ID, id string) (Download, error) {
	res, err := d.Get(ctx, actor, tripID, id)
	if err != nil {
		return Download{}, err
	}
	doc := res.Entity
	if doc.Status != StatusReady {
		return Download{}, notFound()
	}
	signed, err := d.storage.PresignDownload(ctx, doc.StorageKey, DownloadRequest{FileName: doc.FileName, ContentType: doc.MimeType, TTL: DownloadTTL})
	if err != nil {
		return Download{}, fmt.Errorf("presign download: %w", err)
	}
	return Download{Document: doc, Request: signed}, nil
}
