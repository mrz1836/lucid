package router

import (
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/storage"
)

// SecretAddRequest carries one names-only catalog registration. Note is
// optional free text. Now and Source stamp the append-only event; a zero Now
// and empty Source use the local CLI defaults shared by the other router
// intents.
type SecretAddRequest struct {
	Name   string
	Note   string
	Now    time.Time
	Source string
}

// SecretAddResult reports the reference event that was appended and the
// acknowledgement to show the user.
type SecretAddResult struct {
	Event storage.SecretRefEvent
	Ack   string
}

// SecretAdd registers a name in the reference catalog. All request validation
// happens before the catalog is scaffolded, and an already-live name is
// refused without appending another event.
func (r *Router) SecretAdd(req SecretAddRequest) (SecretAddResult, error) {
	if err := storage.ValidateSecretRefName(req.Name); err != nil {
		return SecretAddResult{}, fmt.Errorf("could not register the reference; nothing was saved: %w", err)
	}
	if err := storage.ValidateSecretRefNote(req.Note); err != nil {
		return SecretAddResult{}, fmt.Errorf("could not register the reference; nothing was saved: %w", err)
	}
	source, err := resolveSource(req.Source)
	if err != nil {
		return SecretAddResult{}, fmt.Errorf("invalid source; nothing was saved: %w", err)
	}
	if prepareErr := r.prepareSecrets(); prepareErr != nil {
		return SecretAddResult{}, prepareErr
	}

	refs, err := r.store.ListSecretRefs()
	if err != nil {
		return SecretAddResult{}, err
	}
	if secretRefIsLive(refs, req.Name) {
		return SecretAddResult{}, fmt.Errorf("reference %q is already registered; nothing was saved", req.Name)
	}

	event, err := r.store.AppendSecretRefEvent(storage.SecretRefEvent{
		Name:   req.Name,
		Op:     storage.SecretRefOpRegister,
		Note:   req.Note,
		At:     whenOr(req.Now).Format(time.RFC3339),
		Source: source,
	})
	if err != nil {
		return SecretAddResult{}, fmt.Errorf("could not register the reference; nothing was saved: %w", err)
	}
	return SecretAddResult{
		Event: event,
		Ack:   fmt.Sprintf("Registered reference %s.", event.Name),
	}, nil
}

// SecretList returns the live, name-sorted reference catalog.
func (r *Router) SecretList() ([]storage.SecretRef, error) {
	return r.store.ListSecretRefs()
}

// SecretRemoveResult reports the tombstone event that was appended and the
// acknowledgement to show the user.
type SecretRemoveResult struct {
	Event storage.SecretRefEvent
	Ack   string
}

// SecretRemove appends a tombstone for a live catalog name. A missing name is
// refused rather than treated as a successful no-op.
func (r *Router) SecretRemove(name string, now time.Time, sourceRaw string) (SecretRemoveResult, error) {
	if err := storage.ValidateSecretRefName(name); err != nil {
		return SecretRemoveResult{}, fmt.Errorf("could not remove the reference; nothing was saved: %w", err)
	}
	source, err := resolveSource(sourceRaw)
	if err != nil {
		return SecretRemoveResult{}, fmt.Errorf("invalid source; nothing was saved: %w", err)
	}
	if prepareErr := r.prepareSecrets(); prepareErr != nil {
		return SecretRemoveResult{}, prepareErr
	}

	refs, err := r.store.ListSecretRefs()
	if err != nil {
		return SecretRemoveResult{}, err
	}
	if !secretRefIsLive(refs, name) {
		return SecretRemoveResult{}, fmt.Errorf("reference %q is not registered; nothing was removed", name)
	}

	event, err := r.store.AppendSecretRefEvent(storage.SecretRefEvent{
		Name:   name,
		Op:     storage.SecretRefOpRemove,
		At:     whenOr(now).Format(time.RFC3339),
		Source: source,
	})
	if err != nil {
		return SecretRemoveResult{}, fmt.Errorf("could not remove the reference; nothing was removed: %w", err)
	}
	return SecretRemoveResult{
		Event: event,
		Ack:   fmt.Sprintf("Removed reference %s.", event.Name),
	}, nil
}

func (r *Router) prepareSecrets() error {
	if err := r.store.ScaffoldSecrets(); err != nil {
		return fmt.Errorf("could not prepare the reference catalog: %w", err)
	}
	return nil
}

func secretRefIsLive(refs []storage.SecretRef, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}
