package generator

import (
	"testing"

	"mirror-studio/internal/config"
)

// The lease (and the stall window it derives from) has to cover the client
// timeout the attempt really uses. With the default channel timeout of 60s and
// the PROVIDER_TIMEOUT_SECONDS floor of 300s the lease was 180s, so a slow
// upstream call outlived its own lease: claimTask treats a running task with an
// expired lease as claimable, and a second worker re-ran — and re-billed — the
// same task.
func TestStallWindowCoversTheClientTimeoutFloor(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.ProviderTimeoutSeconds = 300 })
	channel := &Channel{TimeoutSeconds: 60}

	generation := openAIGenerationTimeout(channel, env.cfg.ProviderTimeoutSeconds)
	textTask := &Task{Channel: channel, GenerationMode: ModeTextToImage}
	if lease := env.service.taskLeaseTimeoutSeconds(textTask); lease < generation+60 {
		t.Fatalf("text-to-image lease = %ds, want at least the %ds client timeout plus margin",
			lease, generation)
	}

	edit := openAIEditTimeout(channel, env.cfg.OpenAIImageEditTimeout)
	editTask := &Task{Channel: channel, GenerationMode: ModeImageToImage}
	if lease := env.service.taskLeaseTimeoutSeconds(editTask); lease < edit+60 {
		t.Fatalf("image-to-image lease = %ds, want at least the %ds client timeout plus margin",
			lease, edit)
	}
}

// A task whose channel is not loaded yet keeps the reference window.
func TestStallWindowWithoutAChannel(t *testing.T) {
	env := newTestEnv(t)
	task := &Task{GenerationMode: ModeTextToImage}
	if lease := env.service.taskLeaseTimeoutSeconds(task); lease < minLeaseSeconds {
		t.Fatalf("lease = %ds, want at least %ds", lease, minLeaseSeconds)
	}
}
