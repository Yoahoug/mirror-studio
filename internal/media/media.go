// Package media owns the image library: local object storage, preview
// thumbnails, signed download links, the resumable chunked upload protocol and
// the SSRF-guarded remote image fetch.
//
// The reference implementation had a second MinIO backend selected by
// `storage.mode`; production always ran the local filesystem branch, so 0.0.1
// keeps a single backend rooted at cfg.MediaRoot (review P2-11). Object keys
// keep the reference layout because they are stored in the database and take
// part in the compared snapshot.
package media

import (
	"log/slog"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/siteconfig"
)

// Values written into the database. `bucket_name` and `storage_provider` are
// columns of mediahub_generatedimage and are compared row by row, so the
// reference literals are part of the contract.
const (
	BucketName           = "generated-images"
	StorageProviderLocal = "local"

	StatusAvailable = "available"
	StatusExpired   = "expired"
	StatusDeleted   = "deleted"

	UploadStatusUploading = "uploading"
	UploadStatusCompleted = "completed"
	UploadStatusCanceled  = "canceled"
	UploadStatusExpired   = "expired"

	KindGenerationReferenceImage = "generation_reference_image"
	KindNovelEPUB                = "novel_epub"
)

const (
	resumableUploadRootDir = "uploads/resumable"
	referenceUploadToken   = "generator.reference-upload"
	previewSignerSalt      = ":mediahub-preview"
	downloadTokenMaxAge    = 300 * time.Second
	remoteRedirectLimit    = 3
	remoteImageTimeout     = 60 * time.Second
	// Temporary reference objects are removed once their upload token can no
	// longer be resolved.
	referenceUploadExpirySeconds = 1800
)

// Image is one row of the media library joined with the task metadata the
// public payload exposes.
type Image struct {
	ID               int64
	TaskID           int64
	UserID           int64
	TaskNo           string
	Prompt           string
	Status           string
	ImageIndex       int
	Width            int
	Height           int
	FileSize         int64
	MimeType         string
	ObjectKey        string
	BucketName       string
	PreviewObjectKey string
	SHA256           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ExpiresAt        time.Time
	DeletedAt        *time.Time
}

// StoreImageInput is what the generation worker hands over once an upstream
// image has been decoded.
type StoreImageInput struct {
	UserID   int64
	TaskID   int64
	TaskNo   string
	Index    int
	Content  []byte
	MimeType string
	Prompt   string
}

// Deps are the collaborators the media service needs.
type Deps struct {
	DB     *db.DB
	Config *config.Config
	Site   *siteconfig.Service
	Logger *slog.Logger
}

// Service implements the media domain. It is safe for concurrent use.
//
// Media settings come from the process configuration, not from siteconfig: the
// reference reads IMAGE_* and RESUMABLE_* from the environment for every one of
// these code paths, and the console's site.image_retention_days row was dead in
// 1.x as well. Deps.Site is kept because main.go wires it.
type Service struct {
	db   *db.DB
	cfg  *config.Config
	site *siteconfig.Service
	log  *slog.Logger
}

// NewService builds the media service.
func NewService(deps Deps) *Service {
	return &Service{db: deps.DB, cfg: deps.Config, site: deps.Site, log: deps.Logger}
}

// Logger returns the service logger, never nil.
func (s *Service) logger() *slog.Logger {
	if s.log == nil {
		return slog.Default()
	}
	return s.log
}
