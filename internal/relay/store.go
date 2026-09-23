package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

func validateScopeIDs(projectID, harnessID, sessionID string) error {
	for _, id := range []struct {
		value  string
		prefix byte
	}{
		{projectID, 'p'},
		{harnessID, 'h'},
		{sessionID, 's'},
	} {
		if len(id.value) != 34 || id.value[0] != id.prefix || id.value[1] != '-' {
			return errInvalidRequest
		}
		for i := 2; i < len(id.value); i++ {
			if (id.value[i] < '0' || id.value[i] > '9') && (id.value[i] < 'a' || id.value[i] > 'f') {
				return errInvalidRequest
			}
		}
	}
	return nil
}

type scopeIDs struct {
	projectID string
	harnessID string
	sessionID string
}

type projectCreate struct {
	participantID      string
	recipientID        string
	recipientPublicKey string
}

type projectState struct {
	scope                 scopeIDs
	projectEpoch          int
	participantID         string
	publicKey             string
	signingKeyID          string
	recipientID           string
	recipientPublicKey    string
	recipientSigningKeyID string
	seed                  []byte
	recoveryWarning       bool
}

type projectParticipant struct {
	ParticipantID string `json:"participant_id"`
	PublicKey     string `json:"public_key"`
	SigningKeyID  string `json:"signing_key_id"`
}

type projectMarker struct {
	Administrator string               `json:"administrator"`
	Participants  []projectParticipant `json:"participants"`
	ProjectEpoch  int                  `json:"project_epoch"`
	ProjectID     string               `json:"project_id"`
	Version       int                  `json:"version"`
}

const maxProjectMarkerSize = 4096

var (
	errProjectExists  = errors.New("project_exists")
	errProjectMissing = errors.New("project_missing")
	errInvalidProject = errors.New("invalid_project")
)

func initializeProject(rootPath string, scope scopeIDs, create *projectCreate) (state projectState, err error) {
	if validateScopeIDs(scope.projectID, scope.harnessID, scope.sessionID) != nil {
		return projectState{}, errInvalidRequest
	}
	if create == nil {
		return openProject(rootPath, scope)
	}
	if !validParticipantID(create.participantID) || !validParticipantID(create.recipientID) ||
		create.participantID == create.recipientID {
		return projectState{}, errInvalidRequest
	}
	recipientPublic, valid := decodeProjectPublicKey(create.recipientPublicKey)
	if !valid {
		return projectState{}, errInvalidRequest
	}

	if mkdirErr := os.Mkdir(rootPath, 0o700); mkdirErr != nil {
		if !errors.Is(mkdirErr, fs.ErrExist) {
			return projectState{}, fmt.Errorf("%w: create root: %v", errStorage, mkdirErr)
		}
		rootInfo, statErr := os.Lstat(rootPath)
		if statErr != nil {
			return projectState{}, fmt.Errorf("%w: inspect root: %v", errStorage, statErr)
		}
		if !rootInfo.IsDir() {
			return projectState{}, errInvalidProject
		}
	}
	root, openErr := os.OpenRoot(rootPath)
	if openErr != nil {
		return projectState{}, fmt.Errorf("%w: open root: %v", errStorage, openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			state = projectState{}
			err = fmt.Errorf("%w: close root: %v", errStorage, closeErr)
		}
	}()

	if mkdirErr := root.Mkdir("projects", 0o700); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
		return projectState{}, fmt.Errorf("%w: create projects directory: %v", errStorage, mkdirErr)
	}
	projectDirectory := filepath.Join("projects", scope.projectID)
	if mkdirErr := root.Mkdir(projectDirectory, 0o700); mkdirErr != nil {
		if errors.Is(mkdirErr, fs.ErrExist) {
			return projectState{}, errProjectExists
		}
		return projectState{}, fmt.Errorf("%w: create project directory: %v", errStorage, mkdirErr)
	}
	seedDirectory := filepath.Join(projectDirectory, "local", "harnesses", scope.harnessID, "sessions", scope.sessionID)
	for _, directory := range []string{
		filepath.Join(projectDirectory, "events"),
		filepath.Join(projectDirectory, "events", create.participantID),
		filepath.Join(projectDirectory, "local"),
		filepath.Join(projectDirectory, "local", "harnesses"),
		filepath.Join(projectDirectory, "local", "harnesses", scope.harnessID),
		filepath.Join(projectDirectory, "local", "harnesses", scope.harnessID, "sessions"),
		seedDirectory,
	} {
		if mkdirErr := root.Mkdir(directory, 0o700); mkdirErr != nil {
			return projectState{}, fmt.Errorf("%w: create project state directory: %v", errStorage, mkdirErr)
		}
	}

	seed := make([]byte, ed25519.SeedSize)
	if _, randomErr := rand.Read(seed); randomErr != nil {
		return projectState{}, fmt.Errorf("%w: generate identity seed: %v", errStorage, randomErr)
	}
	localPublic := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if bytes.Equal(localPublic, recipientPublic) {
		return projectState{}, fmt.Errorf("%w: generated duplicate public key", errStorage)
	}
	localPublicText := base64.RawURLEncoding.EncodeToString(localPublic)
	localSigningKeyID := projectSigningKeyID(localPublic)
	recipientSigningKeyID := projectSigningKeyID(recipientPublic)

	marker, marshalErr := json.Marshal(projectMarker{
		Administrator: create.participantID,
		Participants: []projectParticipant{
			{create.participantID, localPublicText, localSigningKeyID},
			{create.recipientID, create.recipientPublicKey, recipientSigningKeyID},
		},
		ProjectEpoch: 1,
		ProjectID:    scope.projectID,
		Version:      1,
	})
	if marshalErr != nil {
		return projectState{}, fmt.Errorf("%w: encode project marker: %v", errStorage, marshalErr)
	}
	marker, canonicalErr := canonicalizeJSON(marker)
	if canonicalErr != nil {
		return projectState{}, fmt.Errorf("%w: canonicalize project marker: %v", errStorage, canonicalErr)
	}

	seedResult, publishErr := publish(context.Background(), filepath.Join(rootPath, seedDirectory), "identity.seed", seed)
	if publishErr != nil {
		return projectState{}, fmt.Errorf("publish identity seed: %w", setupPublicationError(publishErr))
	}
	markerResult, publishErr := publish(context.Background(), filepath.Join(rootPath, projectDirectory), "project.json", marker)
	if publishErr != nil {
		return projectState{}, fmt.Errorf("publish project marker: %w", setupPublicationError(publishErr))
	}

	return projectState{
		scope:                 scope,
		projectEpoch:          1,
		participantID:         create.participantID,
		publicKey:             localPublicText,
		signingKeyID:          localSigningKeyID,
		recipientID:           create.recipientID,
		recipientPublicKey:    create.recipientPublicKey,
		recipientSigningKeyID: recipientSigningKeyID,
		seed:                  append([]byte(nil), seed...),
		recoveryWarning:       publicationWarning(seedResult, markerResult),
	}, nil
}

func openProject(rootPath string, scope scopeIDs) (state projectState, err error) {
	rootInfo, statErr := os.Lstat(rootPath)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return projectState{}, errProjectMissing
		}
		return projectState{}, fmt.Errorf("%w: inspect root: %v", errStorage, statErr)
	}
	if !rootInfo.IsDir() {
		return projectState{}, errInvalidProject
	}

	root, openErr := os.OpenRoot(rootPath)
	if openErr != nil {
		return projectState{}, fmt.Errorf("%w: open root: %v", errStorage, openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			state = projectState{}
			err = fmt.Errorf("%w: close root: %v", errStorage, closeErr)
		}
	}()

	projectDirectory := filepath.Join("projects", scope.projectID)
	projectInfo, statErr := root.Lstat(projectDirectory)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return projectState{}, errProjectMissing
		}
		return projectState{}, fmt.Errorf("%w: inspect project directory: %v", errStorage, statErr)
	}
	if !projectInfo.IsDir() {
		return projectState{}, errInvalidProject
	}
	if pathErr := validateProjectEntries(root, projectDirectory, "events", "local", "project.json"); pathErr != nil {
		return projectState{}, pathErr
	}

	localDirectory := filepath.Join(projectDirectory, "local")
	harnessesDirectory := filepath.Join(localDirectory, "harnesses")
	harnessDirectory := filepath.Join(harnessesDirectory, scope.harnessID)
	sessionsDirectory := filepath.Join(harnessDirectory, "sessions")
	seedDirectory := filepath.Join(sessionsDirectory, scope.sessionID)
	for _, directory := range []string{
		"projects",
		filepath.Join(projectDirectory, "events"),
		localDirectory,
		harnessesDirectory,
		harnessDirectory,
		sessionsDirectory,
		seedDirectory,
	} {
		if pathErr := requireProjectPath(root, directory, true); pathErr != nil {
			return projectState{}, pathErr
		}
	}
	for _, directory := range []struct {
		name    string
		allowed string
	}{
		{localDirectory, "harnesses"},
		{harnessesDirectory, scope.harnessID},
		{harnessDirectory, "sessions"},
		{sessionsDirectory, scope.sessionID},
		{seedDirectory, "identity.seed"},
	} {
		if pathErr := validateProjectEntries(root, directory.name, directory.allowed); pathErr != nil {
			return projectState{}, pathErr
		}
	}

	markerBytes, readErr := readProjectFile(root, filepath.Join(projectDirectory, "project.json"), maxProjectMarkerSize)
	if readErr != nil {
		return projectState{}, readErr
	}
	seed, readErr := readProjectFile(root, filepath.Join(seedDirectory, "identity.seed"), ed25519.SeedSize)
	if readErr != nil {
		return projectState{}, readErr
	}
	if len(seed) != ed25519.SeedSize {
		return projectState{}, errInvalidProject
	}

	canonicalMarker, canonicalErr := canonicalizeJSON(markerBytes)
	if canonicalErr != nil || !bytes.Equal(markerBytes, canonicalMarker) {
		return projectState{}, errInvalidProject
	}
	decoder := json.NewDecoder(bytes.NewReader(markerBytes))
	decoder.DisallowUnknownFields()
	var marker projectMarker
	if decodeErr := decoder.Decode(&marker); decodeErr != nil {
		return projectState{}, errInvalidProject
	}
	if decodeErr := decoder.Decode(&struct{}{}); !errors.Is(decodeErr, io.EOF) {
		return projectState{}, errInvalidProject
	}
	if marker.Version != 1 || marker.ProjectEpoch != 1 || marker.ProjectID != scope.projectID ||
		len(marker.Participants) != 2 || marker.Administrator != marker.Participants[0].ParticipantID {
		return projectState{}, errInvalidProject
	}

	local := marker.Participants[0]
	recipient := marker.Participants[1]
	if !validParticipantID(local.ParticipantID) || !validParticipantID(recipient.ParticipantID) ||
		local.ParticipantID == recipient.ParticipantID {
		return projectState{}, errInvalidProject
	}
	localPublic, localValid := decodeProjectPublicKey(local.PublicKey)
	recipientPublic, recipientValid := decodeProjectPublicKey(recipient.PublicKey)
	if !localValid || !recipientValid || bytes.Equal(localPublic, recipientPublic) ||
		local.SigningKeyID != projectSigningKeyID(localPublic) ||
		recipient.SigningKeyID != projectSigningKeyID(recipientPublic) {
		return projectState{}, errInvalidProject
	}
	derivedPublic := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !bytes.Equal(derivedPublic, localPublic) {
		return projectState{}, errInvalidProject
	}
	eventsDirectory := filepath.Join(projectDirectory, "events")
	if pathErr := validateProjectEntries(root, eventsDirectory, local.ParticipantID); pathErr != nil {
		return projectState{}, pathErr
	}
	if pathErr := requireProjectPath(root, filepath.Join(eventsDirectory, local.ParticipantID), true); pathErr != nil {
		return projectState{}, pathErr
	}

	return projectState{
		scope:                 scope,
		projectEpoch:          marker.ProjectEpoch,
		participantID:         local.ParticipantID,
		publicKey:             local.PublicKey,
		signingKeyID:          local.SigningKeyID,
		recipientID:           recipient.ParticipantID,
		recipientPublicKey:    recipient.PublicKey,
		recipientSigningKeyID: recipient.SigningKeyID,
		seed:                  append([]byte(nil), seed...),
	}, nil
}

func validateProjectEntries(root *os.Root, directoryName string, allowed ...string) error {
	directory, err := root.Open(directoryName)
	if err != nil {
		return fmt.Errorf("%w: open project directory: %v", errStorage, err)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return fmt.Errorf("%w: read project directory: %v", errStorage, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: close project directory: %v", errStorage, closeErr)
	}
	for _, entry := range entries {
		name := entry.Name()
		if validTemporaryName(name) {
			continue
		}
		for _, allowedName := range allowed {
			if name == allowedName {
				name = ""
				break
			}
		}
		if name != "" {
			return errInvalidProject
		}
	}
	return nil
}

func validTemporaryName(name string) bool {
	if len(name) != len(".tmp-")+32 || name[:len(".tmp-")] != ".tmp-" {
		return false
	}
	for _, character := range name[len(".tmp-"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func requireProjectPath(root *os.Root, name string, directory bool) error {
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: missing managed path", errInvalidProject)
		}
		return fmt.Errorf("%w: inspect managed path: %v", errStorage, err)
	}
	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("%w: unsafe managed path", errInvalidProject)
	}
	return nil
}

func readProjectFile(root *os.Root, name string, limit int) ([]byte, error) {
	if err := requireProjectPath(root, name, false); err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w: open managed file: %v", errStorage, err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read managed file: %v", errStorage, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close managed file: %v", errStorage, closeErr)
	}
	if len(contents) > limit {
		return nil, errInvalidProject
	}
	return contents, nil
}

func decodeProjectPublicKey(value string) (ed25519.PublicKey, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return decoded, err == nil && len(decoded) == ed25519.PublicKeySize &&
		base64.RawURLEncoding.EncodeToString(decoded) == value
}

func projectSigningKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validParticipantID(id string) bool {
	if len(id) != 34 || id[0] != 'u' || id[1] != '-' {
		return false
	}
	for i := 2; i < len(id); i++ {
		if (id[i] < '0' || id[i] > '9') && (id[i] < 'a' || id[i] > 'f') {
			return false
		}
	}
	return true
}

var (
	errCanceled        = errors.New("canceled")
	errPublishConflict = errors.New("publish conflict")
	errStorage         = errors.New("storage_error")
)

type publicationState uint8

const (
	publicationNew publicationState = iota
	publicationIdentical
)

type publicationResult struct {
	state          publicationState
	cleanupWarning error
}

func setupPublicationError(err error) error {
	if errors.Is(err, errPublishConflict) {
		return errInvalidProject
	}
	return fmt.Errorf("%w: %v", errStorage, err)
}

func publicationWarning(results ...publicationResult) bool {
	for _, result := range results {
		if result.cleanupWarning != nil {
			return true
		}
	}
	return false
}

func publish(ctx context.Context, directory, finalName string, contents []byte) (publicationResult, error) {
	return publishWithRemove(ctx, directory, finalName, contents, os.Remove)
}

func publishWithRemove(ctx context.Context, directory, finalName string, contents []byte, remove func(string) error) (publicationResult, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return publicationResult{}, fmt.Errorf("%w: generate temporary name: %v", errStorage, err)
	}
	temporary := filepath.Join(directory, ".tmp-"+hex.EncodeToString(random[:]))

	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return publicationResult{}, fmt.Errorf("%w: create temporary file: %v", errStorage, err)
	}
	if n, err := file.Write(contents); err != nil || n != len(contents) {
		if err == nil {
			err = io.ErrShortWrite
		}
		file.Close()
		remove(temporary)
		return publicationResult{}, fmt.Errorf("%w: write temporary file: %v", errStorage, err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		remove(temporary)
		return publicationResult{}, fmt.Errorf("%w: sync temporary file: %v", errStorage, err)
	}
	if err := file.Close(); err != nil {
		remove(temporary)
		return publicationResult{}, fmt.Errorf("%w: close temporary file: %v", errStorage, err)
	}

	final := filepath.Join(directory, finalName)
	if err := ctx.Err(); err != nil {
		remove(temporary)
		return publicationResult{}, fmt.Errorf("%w: %v", errCanceled, err)
	}

	state := publicationNew
	if err := os.Link(temporary, final); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			remove(temporary)
			return publicationResult{}, fmt.Errorf("%w: link published file: %v", errStorage, err)
		}

		info, statErr := os.Lstat(final)
		if statErr != nil {
			remove(temporary)
			return publicationResult{}, fmt.Errorf("%w: inspect published file: %v", errStorage, statErr)
		}
		if !info.Mode().IsRegular() {
			remove(temporary)
			return publicationResult{}, errPublishConflict
		}
		existing, openErr := os.Open(final)
		if openErr != nil {
			remove(temporary)
			return publicationResult{}, fmt.Errorf("%w: open published file: %v", errStorage, openErr)
		}
		got, readErr := io.ReadAll(io.LimitReader(existing, int64(len(contents))+1))
		closeErr := existing.Close()
		if readErr != nil {
			remove(temporary)
			return publicationResult{}, fmt.Errorf("%w: read published file: %v", errStorage, readErr)
		}
		if closeErr != nil {
			remove(temporary)
			return publicationResult{}, fmt.Errorf("%w: close published file: %v", errStorage, closeErr)
		}
		if !bytes.Equal(got, contents) {
			remove(temporary)
			return publicationResult{}, errPublishConflict
		}
		state = publicationIdentical
	}

	result := publicationResult{state: state}
	if err := remove(temporary); err != nil {
		result.cleanupWarning = err
	}
	return result, nil
}

var errInvalidEvent = errors.New("invalid_event")

type historyItem struct {
	EventID  string          `json:"event_id"`
	Envelope json.RawMessage `json:"envelope"`
}

type historyResult struct {
	Events            []historyItem `json:"events"`
	NextAfterSequence uint64        `json:"next_after_seq"`
}

type storedEventFile struct {
	name     string
	sequence uint64
	digest   string
}

type verifiedEvent struct {
	eventID              string
	sequence             uint64
	predecessor          *string
	physicalMilliseconds uint64
	logical              uint64
	envelope             []byte
}

func history(rootPath string, state projectState, after uint64) (historyResult, error) {
	_, selected, err := scanAuthorChain(rootPath, state, after)
	if err != nil {
		return historyResult{}, err
	}

	result := historyResult{Events: make([]historyItem, 0, 1), NextAfterSequence: after}
	if selected != nil {
		result.Events = append(result.Events, historyItem{
			EventID:  selected.eventID,
			Envelope: json.RawMessage(append([]byte(nil), selected.envelope...)),
		})
		result.NextAfterSequence = selected.sequence
	}
	return result, nil
}

func scanAuthorChain(rootPath string, state projectState, after uint64) (last, selected *verifiedEvent, err error) {
	root, openErr := os.OpenRoot(rootPath)
	if openErr != nil {
		return nil, nil, fmt.Errorf("%w: open root: %v", errStorage, openErr)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			last = nil
			selected = nil
			err = fmt.Errorf("%w: close root: %v", errStorage, closeErr)
		}
	}()

	directoryName := filepath.Join("projects", state.scope.projectID, "events", state.participantID)
	info, statErr := root.Lstat(directoryName)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil, nil, errInvalidEvent
		}
		return nil, nil, fmt.Errorf("%w: inspect event directory: %v", errStorage, statErr)
	}
	if !info.IsDir() {
		return nil, nil, errInvalidEvent
	}

	directory, openErr := root.Open(directoryName)
	if openErr != nil {
		return nil, nil, fmt.Errorf("%w: open event directory: %v", errStorage, openErr)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return nil, nil, fmt.Errorf("%w: read event directory: %v", errStorage, readErr)
	}
	if closeErr != nil {
		return nil, nil, fmt.Errorf("%w: close event directory: %v", errStorage, closeErr)
	}

	files := make([]storedEventFile, 0, len(entries))
	for _, entry := range entries {
		if validTemporaryName(entry.Name()) {
			continue
		}
		file, ok := parseStoredEventName(entry.Name())
		if !ok {
			return nil, nil, errInvalidEvent
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].sequence == files[j].sequence {
			return files[i].digest < files[j].digest
		}
		return files[i].sequence < files[j].sequence
	})

	for index, eventFile := range files {
		if eventFile.sequence != uint64(index)+1 {
			return nil, nil, errInvalidEvent
		}
		contents, readErr := readStoredEvent(root, filepath.Join(directoryName, eventFile.name))
		if readErr != nil {
			return nil, nil, readErr
		}
		event, validationErr := validateStoredEvent(state, eventFile, contents)
		if validationErr != nil {
			return nil, nil, validationErr
		}
		if last == nil {
			if event.predecessor != nil || event.logical != 0 {
				return nil, nil, errInvalidEvent
			}
		} else if event.predecessor == nil || *event.predecessor != last.eventID ||
			event.physicalMilliseconds < last.physicalMilliseconds ||
			(event.physicalMilliseconds > last.physicalMilliseconds && event.logical != 0) ||
			(event.physicalMilliseconds == last.physicalMilliseconds &&
				(last.logical == maxSafeInteger || event.logical != last.logical+1)) {
			return nil, nil, errInvalidEvent
		}
		if selected == nil && event.sequence > after {
			copy := event
			selected = &copy
		}
		last = &event
	}
	return last, selected, nil
}

func parseStoredEventName(name string) (storedEventFile, bool) {
	const (
		sequenceLength = 16
		digestLength   = 64
		suffix         = ".json"
	)
	if len(name) != sequenceLength+1+digestLength+len(suffix) || name[sequenceLength] != '-' ||
		name[len(name)-len(suffix):] != suffix {
		return storedEventFile{}, false
	}
	for _, character := range name[:sequenceLength] {
		if character < '0' || character > '9' {
			return storedEventFile{}, false
		}
	}
	digest := name[sequenceLength+1 : sequenceLength+1+digestLength]
	if !lowerHex(digest) {
		return storedEventFile{}, false
	}
	sequence, err := strconv.ParseUint(name[:sequenceLength], 10, 64)
	if err != nil || sequence == 0 || sequence > maxSafeInteger {
		return storedEventFile{}, false
	}
	return storedEventFile{name: name, sequence: sequence, digest: digest}, true
}

func readStoredEvent(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errInvalidEvent
		}
		return nil, fmt.Errorf("%w: inspect stored event: %v", errStorage, err)
	}
	if !info.Mode().IsRegular() {
		return nil, errInvalidEvent
	}
	file, err := root.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errInvalidEvent
		}
		return nil, fmt.Errorf("%w: open stored event: %v", errStorage, err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maxRecordSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read stored event: %v", errStorage, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close stored event: %v", errStorage, closeErr)
	}
	if len(contents) == 0 || len(contents) > maxRecordSize {
		return nil, errInvalidEvent
	}
	return contents, nil
}

func validateStoredEvent(state projectState, file storedEventFile, contents []byte) (verifiedEvent, error) {
	canonicalEnvelope, err := canonicalizeJSON(contents)
	if err != nil || !bytes.Equal(contents, canonicalEnvelope) {
		return verifiedEvent{}, errInvalidEvent
	}
	envelope, ok := decodeStoredObject(contents)
	if !ok || !hasExactKeys(envelope, "payload", "signature") {
		return verifiedEvent{}, errInvalidEvent
	}
	payload, payloadOK := envelope["payload"].(map[string]any)
	signatureText, signatureOK := envelope["signature"].(string)
	if !payloadOK || !signatureOK || !hasExactKeys(payload,
		"author", "author_seq", "body", "created", "prev", "project_epoch", "project_id",
		"signing_algorithm", "signing_key_id", "to", "type", "version") {
		return verifiedEvent{}, errInvalidEvent
	}
	created, createdOK := payload["created"].(map[string]any)
	if !createdOK || !hasExactKeys(created, "logical", "physical_ms") {
		return verifiedEvent{}, errInvalidEvent
	}

	author, authorOK := payload["author"].(string)
	sequence, sequenceOK := payload["author_seq"].(uint64)
	body, bodyOK := payload["body"].(string)
	epoch, epochOK := payload["project_epoch"].(uint64)
	projectID, projectOK := payload["project_id"].(string)
	algorithm, algorithmOK := payload["signing_algorithm"].(string)
	keyID, keyOK := payload["signing_key_id"].(string)
	recipient, recipientOK := payload["to"].(string)
	eventType, typeOK := payload["type"].(string)
	version, versionOK := payload["version"].(uint64)
	physical, physicalOK := created["physical_ms"].(uint64)
	logical, logicalOK := created["logical"].(uint64)
	if !authorOK || !sequenceOK || !bodyOK || !epochOK || !projectOK || !algorithmOK || !keyOK ||
		!recipientOK || !typeOK || !versionOK || !physicalOK || !logicalOK ||
		version != 1 || state.projectEpoch != 1 || epoch != uint64(state.projectEpoch) ||
		projectID != state.scope.projectID || eventType != "message" || author != state.participantID ||
		algorithm != "ed25519" || keyID != state.signingKeyID || sequence != file.sequence ||
		sequence == 0 || sequence > maxSafeInteger || physical > maxSafeInteger || logical > maxSafeInteger {
		return verifiedEvent{}, errInvalidEvent
	}
	if _, err := validateMessage(state.recipientID, recipient, body); err != nil {
		return verifiedEvent{}, errInvalidEvent
	}

	var predecessor *string
	switch value := payload["prev"].(type) {
	case nil:
		if sequence != 1 {
			return verifiedEvent{}, errInvalidEvent
		}
	case string:
		if sequence == 1 || !validEventID(value) {
			return verifiedEvent{}, errInvalidEvent
		}
		predecessor = &value
	default:
		return verifiedEvent{}, errInvalidEvent
	}

	var raw struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(contents, &raw); err != nil {
		return verifiedEvent{}, errInvalidEvent
	}
	canonicalPayload, err := canonicalizeJSON(raw.Payload)
	if err != nil || !bytes.Equal(raw.Payload, canonicalPayload) {
		return verifiedEvent{}, errInvalidEvent
	}

	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != file.digest {
		return verifiedEvent{}, errInvalidEvent
	}
	publicKey, validKey := decodeProjectPublicKey(state.publicKey)
	if !validKey || projectSigningKeyID(publicKey) != state.signingKeyID {
		return verifiedEvent{}, errInvalidEvent
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		base64.RawURLEncoding.EncodeToString(signature) != signatureText ||
		!ed25519.Verify(publicKey, append([]byte("IHR-EVENT-V1\x00"), raw.Payload...), signature) {
		return verifiedEvent{}, errInvalidEvent
	}

	return verifiedEvent{
		eventID:              "sha256:" + file.digest,
		sequence:             sequence,
		predecessor:          predecessor,
		physicalMilliseconds: physical,
		logical:              logical,
		envelope:             contents,
	}, nil
}

func decodeStoredObject(input []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, ok := decodeValue(decoder, 0)
	object, objectOK := value.(map[string]any)
	if !ok || !objectOK {
		return nil, false
	}
	_, err := decoder.Token()
	return object, errors.Is(err, io.EOF)
}

func hasExactKeys(object map[string]any, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func validEventID(value string) bool {
	return len(value) == len("sha256:")+64 && value[:len("sha256:")] == "sha256:" && lowerHex(value[len("sha256:"):])
}

func lowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
