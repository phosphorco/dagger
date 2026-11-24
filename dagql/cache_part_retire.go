package dagql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/call"
)

type importedPartRetired struct {
	recipe *call.ID
	cause  error
}

func (e *importedPartRetired) Error() string {
	return "imported part retired; reselect: " + e.cause.Error()
}
func (e *importedPartRetired) Unwrap() error        { return e.cause }
func (e *importedPartRetired) Is(target error) bool { return target == ErrPartReselect }

func (c *Cache) retireUnavailableImportedPart(ctx context.Context, res AnyResult, address PersistedPartAddress, demand *PartDemandState) error {
	source, candidates, err := c.scanPartSources(ctx, res, address, demand)
	if err != nil {
		return err
	}
	if source != nil {
		return errors.Join(partRefused("retire: a part source appeared"), source.Release(ctx))
	}
	row := res.cacheSharedResult()
	c.egraphMu.RLock()
	imported := row.imported
	c.egraphMu.RUnlock()
	key, _ := partAddressKey(address)
	reasons := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		reason := candidate.offerRefusal
		switch {
		case candidate.unready:
			reason = "capture not ready"
		case candidate.probe == nil:
			reason = "part not described"
		case candidate.probe.Busy:
			reason = "part busy"
		case candidate.offer != nil && demand.exhausted(candidate.row.id, address, candidate.offer, candidate.facts.offers):
			reason = "offer exhausted for this demand"
		case candidate.offer != nil:
			expired, missing := 0, 0
			for _, layer := range candidate.offer.Chain.Layers {
				a, ok := candidate.offer.Chain.Addresses[layer.Descriptor.Digest]
				if !ok || a.URL == "" {
					missing++
				} else if a.ExpiresAtUnix != 0 && a.ExpiresAtUnix <= time.Now().Unix() {
					expired++
				}
			}
			reason = fmt.Sprintf("offer unavailable: %d expired, %d missing addresses; renewal key=%t, bridge=%t", expired, missing, candidate.offer.Chain.RenewalKey != "", c.PartContentSource().attachedBridge() != nil)
		}
		reasons = append(reasons, fmt.Sprintf("candidate %d: %s", candidate.row.id, reason))
	}
	cause := errors.Join(fmt.Errorf("%w: %s; %s", ErrUnavailablePart, key, strings.Join(reasons, "; ")), demand.causes())
	frame := row.loadResultCall()
	unsupported := func(reason string) error {
		return fmt.Errorf("ImportedPartRecoveryUnsupported (%s): %w", reason, cause)
	}
	if !imported {
		return unsupported("receiver is not imported")
	}
	if len(address.OutputPath) != 0 {
		return unsupported("inline output")
	}
	if frame == nil || frame.Kind != ResultCallKindField || frame.Nth != 0 {
		return unsupported("no directly replayable field call")
	}
	if CurrentDagqlServer(ctx) == nil {
		return unsupported("no dagql server")
	}
	recipe, err := frame.recipeID(ctx, c)
	if err != nil {
		return errors.Join(unsupported("recipe cannot be reconstructed"), err)
	}
	blocked := CurrentDagqlServer(ctx).ClassifyRecipe(recipe).NotReplayable
	session, err := partSession(ctx)
	if err != nil {
		return err
	}
	lookup, err := c.partLookupFor(row)
	if err != nil {
		return err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if c.resultsByID[row.id] != row || !row.imported || row.loadResultCall() != frame {
		return partRefused("retire: receiver changed or is not imported")
	}
	current := c.collectPartCandidatesLocked(row, lookup, session)
	if len(current) != len(candidates) {
		return partRefused("retire: candidate set changed")
	}
	for i, candidate := range candidates {
		if current[i].row != candidate.row || current[i].route != candidate.route || c.partFactsLocked(candidate.row) != candidate.facts {
			return partRefused("retire: candidate facts changed")
		}
	}
	// Retirement drops the recipe index; expiry also excludes the row from
	// digest and structural lookup, while existing holders keep their value.
	row.expiresAtUnix = time.Now().Add(-time.Second).Unix()
	c.retireResultLocked(row)
	if blocked != nil {
		return unsupported(fmt.Sprintf("field %s is not replayable: %s", blocked.Field, blocked.Reason))
	}
	return &importedPartRetired{recipe: recipe, cause: cause}
}
