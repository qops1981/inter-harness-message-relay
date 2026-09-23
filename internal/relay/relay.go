package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type singleStringFlag struct {
	value string
	set   bool
}

func (value *singleStringFlag) String() string { return value.value }

func (value *singleStringFlag) Set(text string) error {
	if value.set {
		return errInvalidRequest
	}
	value.value = text
	value.set = true
	return nil
}

// Run executes one relay process and returns its process exit code.
func Run(ctx context.Context, args []string, input io.Reader, output, logs io.Writer) int {
	status := newProcessStatus(time.Now())
	writeLog(logs, time.Now(), 0, "startup", logFields{})

	config, command, err := parseStartup(args)
	session := newSession(config, status)
	var exit int
	if err != nil {
		exit = finishDirect(output, logs, session, nil, "", nil, "", false, err)
	} else if command[0] == "stdio" {
		if len(command) != 1 {
			exit = finishDirect(output, logs, session, nil, "", nil, "", false, errInvalidRequest)
		} else {
			exit = operateConnectionSession(ctx, input, output, logs, session)
		}
	} else {
		exit = operateDirect(ctx, command, output, logs, session)
	}

	writeLog(logs, time.Now(), 0, "shutdown", logFields{})
	return exit
}

func parseStartup(args []string) (connectionConfig, []string, error) {
	flags := flag.NewFlagSet("relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".ihr", "")
	projectID := flags.String("project-id", "", "")
	harnessID := flags.String("harness-id", "", "")
	sessionID := flags.String("session-id", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() == 0 {
		return connectionConfig{}, nil, errInvalidRequest
	}
	if err := validateScopeIDs(*projectID, *harnessID, *sessionID); err != nil {
		return connectionConfig{}, nil, err
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return connectionConfig{}, nil, fmt.Errorf("%w: resolve root: %v", errInternalError, err)
	}
	return connectionConfig{
		root: absoluteRoot,
		scope: scopeIDs{
			projectID: *projectID,
			harnessID: *harnessID,
			sessionID: *sessionID,
		},
	}, flags.Args(), nil
}

func operateDirect(ctx context.Context, args []string, output, logs io.Writer, session *session) int {
	operation := args[0]
	switch operation {
	case "initialize", "send", "history", "status", "shutdown":
	default:
		return finishDirect(output, logs, session, nil, "", nil, "", false, errInvalidRequest)
	}

	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var id, params singleStringFlag
	flags.Var(&id, "id", "")
	flags.Var(&params, "params", "")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !id.set || !params.set || len(params.value) == 0 {
		return finishDirect(output, logs, session, nil, "", nil, "", false, errInvalidRequest)
	}
	var requestID *string
	if validRequestID(id.value) {
		requestID = &id.value
	}
	if len(params.value) > maxRecordSize {
		return finishDirect(output, logs, session, requestID, operation, nil, "", false, errRecordTooLarge)
	}

	record := directRequest(operation, id.value, params.value)
	request, err := decodeRequest(record)
	if err == nil && operation != "initialize" {
		session.project, err = openProject(session.config.root, session.config.scope)
		if err == nil {
			session.initialized = true
		}
	}
	var result any
	var eventID string
	var recoveryWarning bool
	if err == nil {
		result, eventID, err = session.operate(ctx, request)
		recoveryWarning = session.pendingRecoveryWarning
	}
	return finishDirect(output, logs, session, requestID, operation, result, eventID, recoveryWarning, err)
}

func directRequest(operation, id, params string) []byte {
	prefix := `{"version":1,"id":`
	if operation == "initialize" {
		prefix = `{"id":`
	}
	encodedID, _ := json.Marshal(id)
	record := append([]byte(prefix), encodedID...)
	record = append(record, `,"op":"`+operation+`","params":`...)
	record = append(record, params...)
	return append(record, '}')
}

func finishDirect(output, logs io.Writer, session *session, id *string, operation string, result any, eventID string, recoveryWarning bool, operationErr error) int {
	if err := session.writeOutcome(output, logs, id, operation, result, eventID, recoveryWarning, operationErr); err != nil {
		return 1
	}
	if operationErr == nil {
		return 0
	}
	if errors.Is(operationErr, errStorage) || errors.Is(operationErr, errInternalError) || publicError(operationErr).Code == "internal_error" {
		return 1
	}
	return 2
}

type connectionConfig struct {
	root  string
	scope scopeIDs
}

type initializeResult struct {
	ProjectID     string   `json:"project_id"`
	ProjectEpoch  int      `json:"project_epoch"`
	ParticipantID string   `json:"participant_id"`
	PublicKey     string   `json:"public_key"`
	SigningKeyID  string   `json:"signing_key_id"`
	RecipientID   string   `json:"recipient_id"`
	Capabilities  []string `json:"capabilities"`
}

type sendResult struct {
	EventID string `json:"event_id"`
}

type session struct {
	config                 connectionConfig
	status                 *processStatus
	project                projectState
	initialized            bool
	pendingRecoveryWarning bool
}

func newSession(config connectionConfig, status *processStatus) *session {
	return &session{config: config, status: status}
}

func (session *session) operate(ctx context.Context, request decodedRequest) (result any, eventID string, err error) {
	session.pendingRecoveryWarning = false
	if err := validateProtocol(request, session.initialized); err != nil {
		return nil, "", err
	}

	switch request.op {
	case "initialize":
		var create *projectCreate
		if value, ok := request.params["create"].(map[string]any); ok {
			create = &projectCreate{
				participantID:      value["participant_id"].(string),
				recipientID:        value["recipient_id"].(string),
				recipientPublicKey: value["recipient_public_key"].(string),
			}
		}
		session.project, err = initializeProject(session.config.root, session.config.scope, create)
		session.pendingRecoveryWarning = session.project.recoveryWarning
		if err != nil {
			return nil, "", err
		}
		session.initialized = true
		return initializeResult{
			ProjectID:     session.project.scope.projectID,
			ProjectEpoch:  session.project.projectEpoch,
			ParticipantID: session.project.participantID,
			PublicKey:     session.project.publicKey,
			SigningKeyID:  session.project.signingKeyID,
			RecipientID:   session.project.recipientID,
			Capabilities:  []string{"send", "history", "status", "shutdown"},
		}, "", nil
	case "send":
		outcome, sendErr := sendMessage(ctx, session.config.root, session.config.scope,
			request.params["to"].(string), request.params["body"].(string), time.Now().UnixMilli())
		session.pendingRecoveryWarning = outcome.recoveryWarning
		if sendErr == nil {
			session.status.noteSent(outcome.publishedNew)
			result = sendResult{EventID: outcome.eventID}
			eventID = outcome.eventID
		}
		return result, eventID, sendErr
	case "history":
		var after uint64
		if value, ok := request.params["after_seq"].(uint64); ok {
			after = value
		}
		result, err = history(session.config.root, session.project, after)
		return result, "", err
	case "status":
		result, err = session.status.snapshot(time.Now())
		return result, "", err
	case "shutdown":
		return struct{}{}, "", nil
	default:
		return nil, "", errUnsupportedOperation
	}
}

func (session *session) writeOutcome(output, logs io.Writer, id *string, operation string, result any, eventID string, recoveryWarning bool, operationErr error) error {
	normalizedErr := responseClassification(id, result, operationErr)
	if operationErr == nil && normalizedErr != nil {
		result = nil
	}
	operationErr = normalizedErr

	fields := logFields{}
	if id != nil {
		fields.requestID = *id
	}
	message := operation
	level := slog.LevelInfo
	if operationErr != nil {
		public := publicError(operationErr)
		session.status.noteRejected(public.Code, public.Message)
		fields.code = public.Code
		message = "request_rejected"
		level = slog.LevelWarn
	} else {
		fields.projectID = session.project.scope.projectID
		fields.participantID = session.project.participantID
		fields.harnessID = session.project.scope.harnessID
		fields.sessionID = session.project.scope.sessionID
		fields.eventID = eventID
	}
	writeLog(logs, time.Now(), level, message, fields)
	if recoveryWarning {
		fields.code = ""
		writeLog(logs, time.Now(), slog.LevelWarn, "recovery_warning", fields)
	}
	return writeResponse(output, id, result, operationErr)
}

func operateConnection(ctx context.Context, input io.Reader, output, logs io.Writer, config connectionConfig) int {
	return operateConnectionSession(ctx, input, output, logs, newSession(config, newProcessStatus(time.Now())))
}

func operateConnectionSession(ctx context.Context, input io.Reader, output, logs io.Writer, session *session) int {
	reader := bufio.NewReader(input)
	for {
		record, err := readRecord(reader)
		if errors.Is(err, io.EOF) {
			return 0
		}
		if err != nil {
			if session.writeOutcome(output, logs, nil, "", nil, "", false, err) != nil {
				return 1
			}
			if errors.Is(err, errRecordTooLarge) || errors.Is(err, errTruncatedRecord) {
				return 2
			}
			return 1
		}

		id := safeRequestID(record)
		request, operationErr := decodeRequest(record)
		if operationErr == nil {
			id = &request.id
		}

		var result any
		var eventID string
		var recoveryWarning bool
		if operationErr == nil {
			result, eventID, operationErr = session.operate(ctx, request)
			recoveryWarning = session.pendingRecoveryWarning
		}
		if session.writeOutcome(output, logs, id, request.op, result, eventID, recoveryWarning, operationErr) != nil {
			return 1
		}
		if operationErr == nil && request.op == "shutdown" {
			return 0
		}
	}
}

func safeRequestID(record []byte) *string {
	object, ok := decodeStoredObject(record)
	if !ok {
		return nil
	}
	id, ok := object["id"].(string)
	if !ok || !validRequestID(id) {
		return nil
	}
	return &id
}

type processStatus struct {
	startedAt time.Time
	sent      uint64
	rejected  uint64
	lastError *statusError
}

type statusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type statusSnapshot struct {
	StartedAtMilliseconds int64        `json:"started_at_ms"`
	UptimeMilliseconds    int64        `json:"uptime_ms"`
	Sent                  uint64       `json:"sent"`
	Rejected              uint64       `json:"rejected"`
	LastError             *statusError `json:"last_error"`
}

func newProcessStatus(start time.Time) *processStatus {
	return &processStatus{startedAt: start}
}

func (status *processStatus) snapshot(now time.Time) (statusSnapshot, error) {
	startedAtMilliseconds := status.startedAt.UTC().UnixMilli()
	if startedAtMilliseconds < 0 || startedAtMilliseconds > maxSafeInteger {
		return statusSnapshot{}, errIntegerLimit
	}

	uptime := now.Sub(status.startedAt).Milliseconds()
	if uptime < 0 {
		uptime = 0
	} else if uptime > maxSafeInteger {
		uptime = maxSafeInteger
	}
	return statusSnapshot{
		StartedAtMilliseconds: startedAtMilliseconds,
		UptimeMilliseconds:    uptime,
		Sent:                  status.sent,
		Rejected:              status.rejected,
		LastError:             status.lastError,
	}, nil
}

func (status *processStatus) noteSent(isNew bool) {
	if isNew && status.sent < maxSafeInteger {
		status.sent++
	}
}

func (status *processStatus) noteRejected(code, message string) {
	if status.rejected < maxSafeInteger {
		status.rejected++
	}
	switch code {
	case "invalid_project", "invalid_event", "storage_error", "integer_limit", "internal_error":
		status.lastError = &statusError{Code: code, Message: message}
	}
}

const maxLogSize = 2048

type logFields struct {
	projectID     string
	participantID string
	harnessID     string
	sessionID     string
	requestID     string
	eventID       string
	code          string
}

type boundedLogBuffer struct {
	bytes.Buffer
}

func (buffer *boundedLogBuffer) Write(value []byte) (int, error) {
	if buffer.Len()+len(value) > maxLogSize+1 {
		return 0, io.ErrShortBuffer
	}
	return buffer.Buffer.Write(value)
}

func writeLog(output io.Writer, now time.Time, level slog.Level, message string, fields logFields) {
	if !validLogInput(level, message, fields) {
		level, message, fields = slog.LevelError, "internal_error", logFields{}
	}
	encoded, ok := encodeLog(now, level, message, fields)
	if !ok {
		encoded, _ = encodeLog(now, slog.LevelError, "internal_error", logFields{})
	}
	_, _ = output.Write(encoded)
}

func encodeLog(now time.Time, level slog.Level, message string, fields logFields) ([]byte, bool) {
	var buffer boundedLogBuffer
	handler := slog.NewJSONHandler(&buffer, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
		if attribute.Key == slog.LevelKey {
			attribute.Value = slog.StringValue(strings.ToLower(attribute.Value.String()))
		}
		return attribute
	}})
	record := slog.NewRecord(now, level, message, 0)
	for _, field := range []struct {
		name  string
		value string
	}{
		{"project_id", fields.projectID},
		{"participant_id", fields.participantID},
		{"harness_id", fields.harnessID},
		{"session_id", fields.sessionID},
		{"request_id", fields.requestID},
		{"event_id", fields.eventID},
		{"code", fields.code},
	} {
		if field.value != "" {
			record.AddAttrs(slog.String(field.name, field.value))
		}
	}
	if err := handler.Handle(context.Background(), record); err != nil || buffer.Len() == 0 || buffer.Len()-1 > maxLogSize {
		return nil, false
	}
	return buffer.Bytes(), true
}

func validLogInput(level slog.Level, message string, fields logFields) bool {
	switch level {
	case slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError:
	default:
		return false
	}
	switch message {
	case "startup", "initialize", "send", "history", "status", "shutdown", "request_rejected", "recovery_warning", "internal_error":
	default:
		return false
	}
	return validOptional(fields.projectID, func(value string) bool { return validScopeID(value, 'p') }) &&
		validOptional(fields.participantID, validParticipantID) &&
		validOptional(fields.harnessID, func(value string) bool { return validScopeID(value, 'h') }) &&
		validOptional(fields.sessionID, func(value string) bool { return validScopeID(value, 's') }) &&
		validOptional(fields.requestID, validRequestID) &&
		validOptional(fields.eventID, validEventID) &&
		validOptional(fields.code, validLogCode)
}

func validOptional(value string, valid func(string) bool) bool {
	return value == "" || valid(value)
}

func validScopeID(value string, prefix byte) bool {
	return len(value) == 34 && value[0] == prefix && value[1] == '-' && lowerHex(value[2:])
}

func validLogCode(value string) bool {
	switch value {
	case "invalid_request", "record_too_large", "truncated_record", "unsupported_version", "not_initialized", "already_initialized", "unsupported_operation", "project_exists", "project_missing", "invalid_project", "invalid_recipient", "body_limit", "integer_limit", "invalid_event", "storage_error", "canceled", "internal_error":
		return true
	default:
		return false
	}
}

const maxRecordSize = 131072

var (
	errRecordTooLarge  = errors.New("record_too_large")
	errTruncatedRecord = errors.New("truncated_record")
)

func readRecord(input *bufio.Reader) ([]byte, error) {
	record := make([]byte, 0, maxRecordSize+1)
	for {
		b, err := input.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(record) == 0 {
					return nil, io.EOF
				}
				return nil, errTruncatedRecord
			}
			return nil, errInternalError
		}
		if b == '\n' {
			if len(record) > 0 && record[len(record)-1] == '\r' {
				record = record[:len(record)-1]
			}
			return record, nil
		}
		record = append(record, b)
		if len(record) > maxRecordSize {
			return nil, errRecordTooLarge
		}
	}
}

var errInvalidRequest = errors.New("invalid_request")

type decodedRequest struct {
	id      string
	op      string
	version int
	params  map[string]any
}

func decodeRequest(input []byte) (decodedRequest, error) {
	if !utf8.Valid(input) || validateSurrogateEscapes(input) != nil {
		return decodedRequest{}, errInvalidRequest
	}

	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return decodedRequest{}, errInvalidRequest
	}
	root, ok := decodeObject(decoder, 1)
	if !ok {
		return decodedRequest{}, errInvalidRequest
	}
	if _, err := decoder.Token(); err != io.EOF {
		return decodedRequest{}, errInvalidRequest
	}

	id, idOK := root["id"].(string)
	op, opOK := root["op"].(string)
	params, paramsOK := root["params"].(map[string]any)
	if !idOK || !opOK || !paramsOK || !validRequestID(id) || !validOperation(op) {
		return decodedRequest{}, errInvalidRequest
	}

	request := decodedRequest{id: id, op: op, params: params}
	if op == "initialize" {
		if len(root) != 3 || !validInitializeParams(params) {
			return decodedRequest{}, errInvalidRequest
		}
		return request, nil
	}

	version, versionOK := root["version"].(uint64)
	if len(root) != 4 || !versionOK {
		return decodedRequest{}, errInvalidRequest
	}
	request.version = int(version)
	if !validOperationParams(op, params) {
		return decodedRequest{}, errInvalidRequest
	}
	return request, nil
}

func decodeValue(decoder *json.Decoder, depth int) (any, bool) {
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	switch token := token.(type) {
	case json.Delim:
		if depth == 8 {
			return nil, false
		}
		switch token {
		case '{':
			return decodeObject(decoder, depth+1)
		case '[':
			return decodeArray(decoder, depth+1)
		default:
			return nil, false
		}
	case json.Number:
		value := string(token)
		if !validIntegerToken(value) {
			return nil, false
		}
		number, err := strconv.ParseUint(value, 10, 64)
		return number, err == nil && number <= maxSafeInteger
	case string, bool, nil:
		return token, true
	default:
		return nil, false
	}
}

func decodeObject(decoder *json.Decoder, depth int) (map[string]any, bool) {
	object := make(map[string]any)
	for decoder.More() {
		if len(object) == 16 {
			return nil, false
		}
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, exists := object[name]; exists {
			return nil, false
		}
		value, ok := decodeValue(decoder, depth)
		if !ok {
			return nil, false
		}
		object[name] = value
	}
	token, err := decoder.Token()
	return object, err == nil && token == json.Delim('}')
}

func decodeArray(decoder *json.Decoder, depth int) ([]any, bool) {
	var array []any
	for decoder.More() {
		value, ok := decodeValue(decoder, depth)
		if !ok {
			return nil, false
		}
		array = append(array, value)
	}
	token, err := decoder.Token()
	return array, err == nil && token == json.Delim(']')
}

func validIntegerToken(value string) bool {
	if value == "0" {
		return true
	}
	if len(value) == 0 || value[0] < '1' || value[0] > '9' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func validRequestID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i := range len(value) {
		character := value[i]
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validOperation(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for i := range len(value) {
		if (value[i] < 'a' || value[i] > 'z') && value[i] != '_' {
			return false
		}
	}
	return true
}

func validInitializeParams(params map[string]any) bool {
	versions, ok := params["versions"].([]any)
	if !ok || len(versions) == 0 || len(versions) > 8 || len(params) < 1 || len(params) > 2 {
		return false
	}
	seen := make(map[uint64]struct{}, len(versions))
	for _, value := range versions {
		version, ok := value.(uint64)
		if !ok || version == 0 {
			return false
		}
		if _, duplicate := seen[version]; duplicate {
			return false
		}
		seen[version] = struct{}{}
	}
	if len(params) == 1 {
		return true
	}
	create, ok := params["create"].(map[string]any)
	if !ok || len(create) != 3 {
		return false
	}
	_, participantOK := create["participant_id"].(string)
	_, recipientOK := create["recipient_id"].(string)
	_, keyOK := create["recipient_public_key"].(string)
	return participantOK && recipientOK && keyOK
}

func validOperationParams(operation string, params map[string]any) bool {
	switch operation {
	case "send":
		if len(params) != 2 {
			return false
		}
		_, toOK := params["to"].(string)
		_, bodyOK := params["body"].(string)
		return toOK && bodyOK
	case "history":
		if len(params) == 0 {
			return true
		}
		_, ok := params["after_seq"].(uint64)
		return len(params) == 1 && ok
	case "status", "shutdown":
		return len(params) == 0
	default:
		return true
	}
}

var (
	errUnsupportedVersion   = errors.New("unsupported_version")
	errNotInitialized       = errors.New("not_initialized")
	errAlreadyInitialized   = errors.New("already_initialized")
	errUnsupportedOperation = errors.New("unsupported_operation")
)

func validateProtocol(request decodedRequest, initialized bool) error {
	if request.op == "initialize" {
		supported := false
		for _, version := range request.params["versions"].([]any) {
			if version == uint64(1) {
				supported = true
				break
			}
		}
		if !supported {
			return errUnsupportedVersion
		}
		if initialized {
			return errAlreadyInitialized
		}
		return nil
	}

	if request.version != 1 {
		return errUnsupportedVersion
	}
	switch request.op {
	case "send", "history", "status", "shutdown":
	default:
		return errUnsupportedOperation
	}
	if !initialized {
		return errNotInitialized
	}
	return nil
}

var (
	errInternalError = errors.New("internal_error")
	errOutput        = errors.New("output error")
)

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type successResponse struct {
	Version int     `json:"version"`
	ID      *string `json:"id"`
	OK      bool    `json:"ok"`
	Result  any     `json:"result"`
}

type errorResponse struct {
	Version int           `json:"version"`
	ID      *string       `json:"id"`
	OK      bool          `json:"ok"`
	Error   responseError `json:"error"`
}

func marshalSuccessResponse(id *string, result any) ([]byte, error) {
	encoded, err := json.Marshal(successResponse{Version: 1, ID: id, OK: true, Result: result})
	if err == nil {
		encoded, err = canonicalizeJSON(encoded)
	}
	if err != nil || len(encoded) > maxRecordSize {
		return nil, errInternalError
	}
	return encoded, nil
}

func responseClassification(id *string, result any, operationErr error) error {
	if operationErr != nil {
		return operationErr
	}
	_, err := marshalSuccessResponse(id, result)
	return err
}

func writeResponse(output io.Writer, id *string, result any, operationErr error) error {
	var encoded []byte
	if operationErr == nil {
		encoded, operationErr = marshalSuccessResponse(id, result)
	}
	if operationErr != nil {
		encoded, _ = json.Marshal(errorResponse{Version: 1, ID: id, Error: publicError(operationErr)})
	}
	encoded = append(encoded, '\n')

	n, err := output.Write(encoded)
	if err != nil {
		return fmt.Errorf("%w: %v", errOutput, err)
	}
	if n != len(encoded) {
		return fmt.Errorf("%w: short write", errOutput)
	}
	return nil
}

func publicError(err error) responseError {
	for _, classified := range []struct {
		err     error
		code    string
		message string
	}{
		{errInvalidRequest, "invalid_request", "Invalid request."},
		{errRecordTooLarge, "record_too_large", "Record exceeds limit."},
		{errTruncatedRecord, "truncated_record", "Record lacks LF."},
		{errUnsupportedVersion, "unsupported_version", "Version 1 is required."},
		{errNotInitialized, "not_initialized", "Initialize first."},
		{errAlreadyInitialized, "already_initialized", "Connection is initialized."},
		{errUnsupportedOperation, "unsupported_operation", "Operation is unavailable."},
		{errProjectExists, "project_exists", "Project already exists."},
		{errProjectMissing, "project_missing", "Project does not exist."},
		{errInvalidProject, "invalid_project", "Project state is invalid."},
		{errInvalidRecipient, "invalid_recipient", "Recipient is not registered."},
		{errBodyLimit, "body_limit", "Body length is invalid."},
		{errIntegerLimit, "integer_limit", "Event integer exceeds limit."},
		{errInvalidEvent, "invalid_event", "Stored event is invalid."},
		{errStorage, "storage_error", "Storage operation failed."},
		{errCanceled, "canceled", "Operation was canceled."},
	} {
		if errors.Is(err, classified.err) {
			return responseError{classified.code, classified.message}
		}
	}
	return responseError{"internal_error", "Operation failed."}
}
