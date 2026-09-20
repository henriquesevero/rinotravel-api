package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/document"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/mongostore"
	"rinotravel-api/internal/user"
)

type doc struct {
	OwnerID    string `bson:"ownerId"`
	Name       string `bson:"name"`
	Type       string `bson:"type"`
	FileName   string `bson:"fileName"`
	MimeType   string `bson:"mimeType"`
	Size       int64  `bson:"size"`
	StorageKey string `bson:"storageKey"`
	Checksum   string `bson:"checksum"`
	Status     string `bson:"status"`
	Visibility string `bson:"visibility"`
	LinkType   string `bson:"linkType,omitempty"`
	LinkID     string `bson:"linkId,omitempty"`
}

var codec = mongostore.Codec[document.Document]{
	Base: document.Base,
	Encode: func(d document.Document) bson.D {
		return mongostore.Marshal(doc{
			OwnerID: string(d.OwnerID), Name: d.Name, Type: string(d.Type), FileName: d.FileName, MimeType: d.MimeType,
			Size: d.Size, StorageKey: d.StorageKey, Checksum: d.Checksum, Status: string(d.Status),
			Visibility: string(d.Visibility), LinkType: d.LinkType, LinkID: d.LinkID,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (document.Document, error) {
		var d doc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return document.Document{}, err
		}
		return document.Document{
			Base: base, OwnerID: user.ID(d.OwnerID), Name: d.Name, Type: document.Type(d.Type), FileName: d.FileName,
			MimeType: d.MimeType, Size: d.Size, StorageKey: d.StorageKey, Checksum: d.Checksum,
			Status: document.Status(d.Status), Visibility: document.Visibility(d.Visibility), LinkType: d.LinkType, LinkID: d.LinkID,
		}, nil
	},
}

func NewStore(db *mongo.Database) *mongostore.Store[document.Document] {
	return mongostore.New(db, "documents", codec)
}
