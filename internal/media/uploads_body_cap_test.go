package media

import (
	"bytes"
	"net/http"
	"testing"

	"mirror-studio/internal/config"
)

// The middleware used to buffer at most 4 MiB of any request body while the
// deployment advertises RESUMABLE_UPLOAD_CHUNK_SIZE = 8 MiB. Every full chunk
// was therefore truncated and rejected with "分片大小不匹配", which made any
// upload above 4 MiB impossible — every EPUB and every large reference image.
func TestChunkUploadAcceptsBodiesAboveTheBaseBodyCap(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) {
		cfg.ResumableUploadChunkSize = 6 * 1024 * 1024
	})
	owner := env.user("owner")
	content := append([]byte("PK\x03\x04"), bytes.Repeat([]byte("x"), 5*1024*1024)...)

	session := decodeData[map[string]any](t,
		env.createSession(KindNovelEPUB, "big.epub", "application/epub+zip", len(content), "big-1", owner))
	if chunkSize := int(session["chunk_size"].(float64)); chunkSize != 6*1024*1024 {
		t.Fatalf("chunk_size = %d, want the configured %d", chunkSize, 6*1024*1024)
	}

	complete := env.uploadAllChunks(session, content, owner)
	if complete.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d, want 200", complete.StatusCode)
	}
	result := decodeData[map[string]any](t, decodeEnvelope(t, complete))["result"].(map[string]any)
	if result["size"].(float64) != float64(len(content)) {
		t.Fatalf("result = %+v, want size %d", result, len(content))
	}
}
