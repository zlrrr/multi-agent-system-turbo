package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/core"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// Object is a run store on S3-compatible object storage: durable, and shared by
// every replica rather than private to one pod's disk.
//
// The layout inverts what the filesystem store does. On disk, a step arriving
// rewrites the whole record; on object storage that would be a GET and a PUT of
// the entire run per step, and two writers would silently lose each other's
// updates. Here nothing is rewritten because nothing is written twice:
//
//	<prefix>/runs/<runID>/record.json      written at Create, once more at Finish
//	<prefix>/runs/<runID>/steps/0001.json  written once, never again
//
// That honours the append-only contract literally, and buys something the
// filesystem store never had: a run interrupted between those two writes is
// still readable, because its steps were durable when they happened
// (specs/010-object-run-store/design-hld.md §2).
type Object struct {
	client *s3Client
	prefix string

	mu    sync.Mutex
	steps map[string]int // runID → steps written, for the next key
}

// maxSteps is what four zero-padded digits can order. A run cannot approach it
// — the step budget is three orders of magnitude below — but the writer refuses
// rather than wrapping, because a silently reordered audit trail is worse than
// a refused write.
const maxSteps = 9999

// NewObject builds an object-storage run store.
func NewObject(cfg config.S3Config) (*Object, error) {
	if err := ValidateS3(cfg); err != nil {
		return nil, err
	}
	client, err := newS3Client(cfg)
	if err != nil {
		return nil, err
	}
	return &Object{
		client: client,
		prefix: strings.Trim(strings.TrimSpace(cfg.Prefix), "/"),
		steps:  map[string]int{},
	}, nil
}

func (o *Object) recordKey(runID string) string { return o.runPrefix(runID) + "record.json" }

func (o *Object) runPrefix(runID string) string { return o.runsPrefix() + runID + "/" }

func (o *Object) runsPrefix() string {
	if o.prefix == "" {
		return "runs/"
	}
	return o.prefix + "/runs/"
}

func (o *Object) stepKey(runID string, n int) string {
	return fmt.Sprintf("%ssteps/%04d.json", o.runPrefix(runID), n)
}

// Create writes the initial record.
func (o *Object) Create(ctx context.Context, rec *core.RunRecord) error {
	blob, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	return o.client.put(ctx, o.recordKey(rec.ID), blob)
}

// Append writes one step as its own immutable object. The record object is not
// touched, which is what makes this append-only on a backend with no append.
func (o *Object) Append(ctx context.Context, runID string, step core.Step) error {
	o.mu.Lock()
	n := o.steps[runID] + 1
	if n > maxSteps {
		o.mu.Unlock()
		return errs.New("MAS-6014", runID, maxSteps)
	}
	o.steps[runID] = n
	o.mu.Unlock()

	if step.ID == "" {
		step.ID = nextStepID(n - 1)
	}
	blob, err := json.Marshal(step)
	if err != nil {
		return errs.Wrap(err, "MAS-6002", runID, err.Error())
	}
	return o.client.put(ctx, o.stepKey(runID, n), blob)
}

// Finish attaches the report and marks the run complete.
func (o *Object) Finish(ctx context.Context, runID string, rep *core.Report, u core.Usage) error {
	rec, err := o.Get(ctx, runID)
	if err != nil {
		return err
	}
	rec.Report = rep
	rec.Usage = u
	rec.Status = core.RunCompleted
	rec.FinishedAt = nowUTC()
	blob, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	return o.client.put(ctx, o.recordKey(runID), blob)
}

// Fail marks a run failed, recording why as its last step.
func (o *Object) Fail(ctx context.Context, runID, code, message string) error {
	note := core.Step{
		Kind: core.StepNote, At: nowUTC(), Actor: "service",
		Name: "run failed", Code: code, Err: message,
	}
	if err := o.Append(ctx, runID, note); err != nil {
		return err
	}
	rec, err := o.Get(ctx, runID)
	if err != nil {
		return err
	}
	rec.Status = core.RunFailed
	rec.FinishedAt = nowUTC()
	blob, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	return o.client.put(ctx, o.recordKey(runID), blob)
}

// Get reads a run.
//
// A finished run is one GET, which is the case worth optimising. A record that
// still says `running` means the process that wrote it is gone or still going,
// and the steps are the truth — so they are listed and merged. The returned
// record keeps `status: running`: it is what was recorded, not a claim that the
// run completed (design-lld.md §6).
func (o *Object) Get(ctx context.Context, runID string) (*core.RunRecord, error) {
	blob, err := o.client.get(ctx, o.recordKey(runID))
	if err != nil {
		return nil, err
	}
	rec, err := decodeRecord(runID, blob)
	if err != nil {
		return nil, err
	}
	if rec.Status != core.RunRunning {
		return rec, nil
	}

	steps, err := o.readSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	if len(steps) > len(rec.Steps) {
		rec.Steps = steps
	}
	return rec, nil
}

// readSteps reads every step object a run wrote, in key order.
func (o *Object) readSteps(ctx context.Context, runID string) ([]core.Step, error) {
	prefix := o.runPrefix(runID) + "steps/"
	var keys []string
	token := ""
	for {
		page, err := o.client.list(ctx, prefix, "", token, 1000)
		if err != nil {
			return nil, err
		}
		for _, c := range page.Contents {
			keys = append(keys, c.Key)
		}
		if !page.IsTruncated || page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	sort.Strings(keys) // zero-padded, so key order is step order

	out := make([]core.Step, 0, len(keys))
	for _, key := range keys {
		blob, err := o.client.get(ctx, key)
		if err != nil {
			return nil, err
		}
		var step core.Step
		if err := json.Unmarshal(blob, &step); err != nil {
			return nil, errs.Wrap(err, "MAS-6013", key, err.Error())
		}
		out = append(out, step)
	}
	return out, nil
}

// List returns run summaries, newest first.
//
// Run ids are `run-<timestamp>-<random>`, so lexicographic descending is
// newest-first — no index to maintain and none to fall out of step with
// reality. The listing stops once it has `limit` run prefixes, so a record that
// will not be returned is never read.
func (o *Object) List(ctx context.Context, limit int) ([]core.RunSummary, error) {
	if limit <= 0 {
		limit = 50
	}

	var runIDs []string
	token := ""
	for {
		page, err := o.client.list(ctx, o.runsPrefix(), "/", token, 1000)
		if err != nil {
			return nil, err
		}
		for _, p := range page.CommonPrefixes {
			id := strings.Trim(strings.TrimPrefix(p.Prefix, o.runsPrefix()), "/")
			if id != "" {
				runIDs = append(runIDs, id)
			}
		}
		if !page.IsTruncated || page.NextToken == "" {
			break
		}
		token = page.NextToken
	}

	sort.Sort(sort.Reverse(sort.StringSlice(runIDs)))
	if len(runIDs) > limit {
		runIDs = runIDs[:limit]
	}

	out := make([]core.RunSummary, 0, len(runIDs))
	for _, id := range runIDs {
		rec, err := o.Get(ctx, id)
		if err != nil {
			// One unreadable record must not hide every other run. It is
			// reported through the doctor probe and the logs, not by making
			// the whole listing fail.
			continue
		}
		out = append(out, rec.Summarise())
	}
	return out, nil
}

// Close releases nothing: the HTTP client has no state worth closing.
func (o *Object) Close() error { return nil }

// Probe reports whether the bucket is reachable, for `mas doctor`.
func (o *Object) Probe(ctx context.Context) error {
	_, err := o.client.list(ctx, o.runsPrefix(), "/", "", 1)
	return err
}

// encodeRecord produces the same digest-wrapped envelope the filesystem store
// writes, so a record means the same thing wherever it is stored.
func encodeRecord(rec *core.RunRecord) ([]byte, error) {
	body, err := json.Marshal(rec)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-6002", rec.ID, err.Error())
	}
	env := envelope{Digest: digest(body), Record: body}
	blob, err := json.Marshal(env)
	if err != nil {
		return nil, errs.Wrap(err, "MAS-6002", rec.ID, err.Error())
	}
	return blob, nil
}

// decodeRecord unwraps and verifies a stored record.
func decodeRecord(runID string, blob []byte) (*core.RunRecord, error) {
	var env envelope
	if err := json.Unmarshal(blob, &env); err != nil {
		return nil, errs.Wrap(err, "MAS-6013", runID, err.Error())
	}
	if env.Digest != digest(env.Record) {
		return nil, errs.New("MAS-6003", runID, "digest mismatch: the record was modified after it was written")
	}
	var rec core.RunRecord
	if err := json.Unmarshal(env.Record, &rec); err != nil {
		return nil, errs.Wrap(err, "MAS-6013", runID, err.Error())
	}
	return &rec, nil
}

// ValidateS3 checks an object-store configuration at load, so a mistake is
// found before the first write rather than during one.
func ValidateS3(cfg config.S3Config) error {
	switch {
	case strings.TrimSpace(cfg.Endpoint) == "":
		return errs.New("MAS-6010", "store.s3.endpoint", "must be set")
	case strings.TrimSpace(cfg.Bucket) == "":
		return errs.New("MAS-6010", "store.s3.bucket", "must be set")
	case strings.TrimSpace(cfg.Region) == "":
		return errs.New("MAS-6010", "store.s3.region", "must be set")
	case cfg.AccessKeyID.IsZero() != cfg.SecretAccessKey.IsZero():
		return errs.New("MAS-6010", "store.s3",
			"access_key_id and secret_access_key must both be set or both be empty")
	}
	if _, err := (&s3Client{cfg: cfg}).endpointFor("", nil); err != nil {
		return err
	}
	return nil
}
