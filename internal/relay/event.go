package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
)

const maxSafeInteger = 9007199254740991

var errIntegerLimit = errors.New("integer_limit")

type eventPosition struct {
	eventID              string
	sequence             uint64
	predecessor          *string
	physicalMilliseconds int64
	logical              uint64
}

func nextEventPosition(prior *eventPosition, wallMilliseconds int64) (eventPosition, error) {
	if wallMilliseconds < 0 || wallMilliseconds > maxSafeInteger {
		return eventPosition{}, errIntegerLimit
	}
	if prior == nil {
		return eventPosition{sequence: 1, physicalMilliseconds: wallMilliseconds}, nil
	}
	if prior.sequence == maxSafeInteger || (wallMilliseconds <= prior.physicalMilliseconds && prior.logical == maxSafeInteger) {
		return eventPosition{}, errIntegerLimit
	}

	next := eventPosition{
		sequence:             prior.sequence + 1,
		predecessor:          &prior.eventID,
		physicalMilliseconds: wallMilliseconds,
	}
	if wallMilliseconds <= prior.physicalMilliseconds {
		next.physicalMilliseconds = prior.physicalMilliseconds
		next.logical = prior.logical + 1
	}
	return next, nil
}

func signEvent(seed, payload []byte) ([]byte, string, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, "", errors.New("invalid Ed25519 seed length")
	}

	canonicalPayload, err := canonicalizeJSON(payload)
	if err != nil {
		return nil, "", err
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(seed), append([]byte("IHR-EVENT-V1\x00"), canonicalPayload...))
	envelope, err := json.Marshal(struct {
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}{canonicalPayload, base64.RawURLEncoding.EncodeToString(signature)})
	if err != nil {
		return nil, "", err
	}
	canonicalEnvelope, err := canonicalizeJSON(envelope)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(canonicalEnvelope)
	return canonicalEnvelope, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func canonicalizeJSON(input []byte) ([]byte, error) {
	if !utf8.Valid(input) {
		return nil, errors.New("JSON is not valid UTF-8")
	}
	if err := validateSurrogateEscapes(input); err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err != nil {
		return nil, err
	}
	return jcs.Transform(compact.Bytes())
}

func validateSurrogateEscapes(input []byte) error {
	inString := false
	for i := 0; i < len(input); i++ {
		if input[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || input[i] != '\\' {
			continue
		}
		if i+1 >= len(input) || input[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(input) {
			return errors.New("incomplete Unicode escape")
		}
		first, err := strconv.ParseUint(string(input[i+2:i+6]), 16, 16)
		if err != nil {
			return errors.New("invalid Unicode escape")
		}
		if first >= 0xdc00 && first <= 0xdfff {
			return errors.New("isolated low surrogate")
		}
		if first < 0xd800 || first > 0xdbff {
			i += 5
			continue
		}
		if i+12 > len(input) || input[i+6] != '\\' || input[i+7] != 'u' {
			return errors.New("isolated high surrogate")
		}
		second, err := strconv.ParseUint(string(input[i+8:i+12]), 16, 16)
		if err != nil || second < 0xdc00 || second > 0xdfff {
			return errors.New("invalid surrogate pair")
		}
		i += 11
	}
	return nil
}

var (
	errBodyLimit        = errors.New("body_limit")
	errInvalidRecipient = errors.New("invalid_recipient")
)

func validateMessage(recipient, to, body string) (string, error) {
	if to != recipient {
		return "", errInvalidRecipient
	}
	if len(body) == 0 || len(body) > 16384 {
		return "", errBodyLimit
	}
	return body, nil
}

type sendOutcome struct {
	eventID         string
	recoveryWarning bool
	publishedNew    bool
}

func sendMessage(ctx context.Context, rootPath string, scope scopeIDs, to, body string, wallMilliseconds int64) (sendOutcome, error) {
	state, err := openProject(rootPath, scope)
	if err != nil {
		return sendOutcome{}, err
	}
	if _, err := validateMessage(state.recipientID, to, body); err != nil {
		return sendOutcome{}, err
	}
	if ctx.Err() != nil {
		return sendOutcome{}, errCanceled
	}

	last, _, err := scanAuthorChain(rootPath, state, 0)
	if err != nil {
		return sendOutcome{}, err
	}
	var prior *eventPosition
	if last != nil {
		prior = &eventPosition{
			eventID:              last.eventID,
			sequence:             last.sequence,
			physicalMilliseconds: int64(last.physicalMilliseconds),
			logical:              last.logical,
		}
	}
	position, err := nextEventPosition(prior, wallMilliseconds)
	if err != nil {
		return sendOutcome{}, err
	}

	payload, err := json.Marshal(map[string]any{
		"author":            state.participantID,
		"author_seq":        position.sequence,
		"body":              body,
		"created":           map[string]any{"logical": position.logical, "physical_ms": position.physicalMilliseconds},
		"prev":              position.predecessor,
		"project_epoch":     state.projectEpoch,
		"project_id":        state.scope.projectID,
		"signing_algorithm": "ed25519",
		"signing_key_id":    state.signingKeyID,
		"to":                state.recipientID,
		"type":              "message",
		"version":           1,
	})
	if err != nil {
		return sendOutcome{}, errInternalError
	}
	envelope, eventID, err := signEvent(state.seed, payload)
	if err != nil || len(envelope) > maxRecordSize {
		return sendOutcome{}, errInternalError
	}

	name := fmt.Sprintf("%016d-%s.json", position.sequence, eventID[len("sha256:"):])
	directory := filepath.Join(rootPath, "projects", state.scope.projectID, "events", state.participantID)
	publication, err := publish(ctx, directory, name, envelope)
	if err != nil {
		if errors.Is(err, errPublishConflict) {
			return sendOutcome{}, errInvalidEvent
		}
		return sendOutcome{}, err
	}
	return sendOutcome{
		eventID:         eventID,
		recoveryWarning: publicationWarning(publication),
		publishedNew:    publication.state == publicationNew,
	}, nil
}
