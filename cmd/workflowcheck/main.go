package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

const (
	windowStart = "2000-01-01T00:00:00Z"
	windowEnd   = "2100-01-01T00:00:00Z"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":          checkRegisterDevice,
		"register-standard":        checkRegisterStandard,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
		"calibration-trace":        checkCalibrationTrace,
		"standard-lifecycle":       checkStandardLifecycle,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkRegisterStandard() error {
	h := newHarness()
	defer h.close()

	root, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":          " std-root ",
		"name":          "National Reference",
		"valid_from":    windowStart,
		"valid_through": windowEnd,
		"custodian":     "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(root, http.StatusCreated); err != nil {
		return err
	}
	rootID, err := nestedString(root.Body, "standard", "id")
	if err != nil {
		return err
	}
	rootCode, err := nestedString(root.Body, "standard", "code")
	if err != nil {
		return err
	}
	if rootCode != "STD-ROOT" {
		return fmt.Errorf("standard code was not normalized: %q", rootCode)
	}

	child, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":          "std-working",
		"name":          "Lab Working Standard",
		"parent_id":     rootID,
		"valid_from":    windowStart,
		"valid_through": windowEnd,
		"custodian":     "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(child, http.StatusCreated); err != nil {
		return err
	}

	duplicate, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":       "STD-ROOT",
		"name":       "Duplicate Root",
		"valid_from": windowStart,
		"custodian":  "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(duplicate, "duplicate_standard_code"); err != nil {
		return err
	}

	broken, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":       "STD-BROKEN",
		"name":       "Orphan Standard",
		"parent_id":  "standard-9999",
		"valid_from": windowStart,
		"custodian":  "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(broken, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(broken, "trace_broken"); err != nil {
		return err
	}

	inverted, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":          "STD-BAD",
		"name":          "Bad Interval",
		"valid_from":    windowEnd,
		"valid_through": windowStart,
		"custodian":     "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(inverted, http.StatusBadRequest); err != nil {
		return err
	}
	return expectErrorCode(inverted, "invalid_input")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	standardID, err := h.createRootStandard("STD-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID, standardID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}
	if err := expectTrace(opened.Body, []string{"STD-1101"}, []string{"root"}); err != nil {
		return err
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   deviceID,
		"standard_id": standardID,
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "active_case_exists"); err != nil {
		return err
	}

	missingDeviceID, err := h.createDevice("MET-1102")
	if err != nil {
		return err
	}
	missing, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   missingDeviceID,
		"standard_id": "standard-9999",
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missing, http.StatusNotFound); err != nil {
		return err
	}
	return expectErrorCode(missing, "not_found")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	standardID, err := h.createRootStandard("STD-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID, standardID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectStandardID, err := h.createRootStandard("STD-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID, rejectStandardID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

// checkCalibrationTrace verifies that a case opened against a three-level chain
// carries the complete working -> parent -> root path, and that sealing freezes
// that path into the evidence seal.
func checkCalibrationTrace() error {
	h := newHarness()
	defer h.close()

	rootID, err := h.createRootStandard("STD-ROOT-1501")
	if err != nil {
		return err
	}
	parentID, err := h.createStandard("STD-REF-1501", rootID)
	if err != nil {
		return err
	}
	workingID, err := h.createStandard("STD-WORK-1501", parentID)
	if err != nil {
		return err
	}

	deviceID, err := h.createDevice("MET-1501")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID, workingID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	if err := expectTrace(opened.Body,
		[]string{"STD-WORK-1501", "STD-REF-1501", "STD-ROOT-1501"},
		[]string{"working", "parent", "root"}); err != nil {
		return err
	}

	measured, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(measured, http.StatusCreated); err != nil {
		return err
	}
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "sealed with full calibration chain",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealCodes, err := traceCodes(sealed.Body, "seal", "trace")
	if err != nil {
		return err
	}
	if len(sealCodes) != 3 || sealCodes[0] != "STD-WORK-1501" || sealCodes[2] != "STD-ROOT-1501" {
		return fmt.Errorf("sealed trace chain = %v", sealCodes)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	return expectTrace(read.Body,
		[]string{"STD-WORK-1501", "STD-REF-1501", "STD-ROOT-1501"},
		[]string{"working", "parent", "root"})
}

// checkStandardLifecycle verifies that deactivating a standard blocks new work
// (opening cases, recording mid-case measurements, sealing review-ready cases)
// while already-sealed evidence keeps reading by the facts frozen at seal time.
func checkStandardLifecycle() error {
	h := newHarness()
	defer h.close()

	// Case A is sealed while the chain is healthy.
	rootAID, err := h.createRootStandard("STD-ROOT-1601")
	if err != nil {
		return err
	}
	workingAID, err := h.createStandard("STD-WORK-1601", rootAID)
	if err != nil {
		return err
	}
	sealedCaseID, err := h.readyCaseWithStandard("MET-1601", workingAID)
	if err != nil {
		return err
	}
	sealed, err := h.request(http.MethodPost, "/cases/"+sealedCaseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "sealed before withdrawal",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}

	// Withdraw the working standard after the seal.
	deactivated, err := h.request(http.MethodPost, "/standards/"+workingAID+"/deactivate", map[string]any{
		"actor": "Steward Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(deactivated, http.StatusOK); err != nil {
		return err
	}
	status, err := nestedString(deactivated.Body, "standard", "status")
	if err != nil {
		return err
	}
	if status != "deactivated" {
		return fmt.Errorf("standard status = %q", status)
	}

	// The sealed evidence still reads by the facts at sealing time.
	read, err := h.request(http.MethodGet, "/cases/"+sealedCaseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	if state, _ := nestedString(read.Body, "case", "state"); state != "sealed" {
		return fmt.Errorf("historical sealed case state = %q", state)
	}
	if err := expectTrace(read.Body,
		[]string{"STD-WORK-1601", "STD-ROOT-1601"},
		[]string{"working", "root"}); err != nil {
		return fmt.Errorf("sealed evidence changed after withdrawal: %w", err)
	}
	frozenStatus, err := nestedString(read.Body, "trace", "links", 0, "status")
	if err != nil {
		return err
	}
	if frozenStatus != "active" {
		return fmt.Errorf("frozen link status was rewritten to %q", frozenStatus)
	}

	// A new case cannot reference the withdrawn standard.
	newDeviceID, err := h.createDevice("MET-1602")
	if err != nil {
		return err
	}
	blockedOpen, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   newDeviceID,
		"standard_id": workingAID,
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedOpen, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedOpen, "standard_inactive"); err != nil {
		return err
	}

	// A case opened while healthy cannot accept measurements after withdrawal.
	rootBID, err := h.createRootStandard("STD-ROOT-1603")
	if err != nil {
		return err
	}
	workingBID, err := h.createStandard("STD-WORK-1603", rootBID)
	if err != nil {
		return err
	}
	deviceBID, err := h.createDevice("MET-1603")
	if err != nil {
		return err
	}
	openedB, err := h.openCase(deviceBID, workingBID)
	if err != nil {
		return err
	}
	caseBID, err := nestedString(openedB.Body, "case", "id")
	if err != nil {
		return err
	}
	if _, err := h.request(http.MethodPost, "/standards/"+workingBID+"/deactivate", map[string]any{
		"actor": "Steward Lin",
	}); err != nil {
		return err
	}
	blockedMeasurement, err := h.recordMeasurement(caseBID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(blockedMeasurement, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedMeasurement, "standard_inactive"); err != nil {
		return err
	}

	// A review-ready case cannot be sealed once its basis lapses.
	rootCID, err := h.createRootStandard("STD-ROOT-1604")
	if err != nil {
		return err
	}
	readyCaseID, err := h.readyCaseWithStandard("MET-1604", rootCID)
	if err != nil {
		return err
	}
	if _, err := h.request(http.MethodPost, "/standards/"+rootCID+"/deactivate", map[string]any{
		"actor": "Steward Lin",
	}); err != nil {
		return err
	}
	blockedSeal, err := h.request(http.MethodPost, "/cases/"+readyCaseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "must not seal on a withdrawn basis",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedSeal, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(blockedSeal, "standard_inactive")
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) createRootStandard(code string) (string, error) {
	return h.createStandard(code, "")
}

func (h *harness) createStandard(code, parentID string) (string, error) {
	body := map[string]any{
		"code":          code,
		"name":          "Standard " + code,
		"valid_from":    windowStart,
		"valid_through": windowEnd,
		"custodian":     "Lin",
	}
	if parentID != "" {
		body["parent_id"] = parentID
	}
	response, err := h.request(http.MethodPost, "/standards", body)
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "standard", "id")
}

func (h *harness) openCase(deviceID, standardID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   deviceID,
		"standard_id": standardID,
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	standardID, err := h.createRootStandard("STD-" + tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID, standardID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	standardID, err := h.createRootStandard("STD-" + tag)
	if err != nil {
		return "", err
	}
	return h.readyCaseWithStandard(tag, standardID)
}

func (h *harness) readyCaseWithStandard(tag, standardID string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID, standardID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func traceCodes(root map[string]any, path ...any) ([]string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%v is not an array", path)
	}
	codes := make([]string, 0, len(items))
	for _, item := range items {
		link, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("trace link is not an object")
		}
		code, ok := link["code"].(string)
		if !ok {
			return nil, errors.New("trace link has no string code")
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func expectTrace(root map[string]any, codes, roles []string) error {
	valid, err := nestedAny(root, "trace", "valid")
	if err != nil {
		return err
	}
	if valid != true {
		reason, _ := nestedString(root, "trace", "reason")
		return fmt.Errorf("trace is not valid (reason=%q)", reason)
	}
	value, err := nestedAny(root, "trace", "links")
	if err != nil {
		return err
	}
	items, ok := value.([]any)
	if !ok {
		return errors.New("trace links is not an array")
	}
	if len(items) != len(codes) {
		return fmt.Errorf("trace length = %d, expected %d", len(items), len(codes))
	}
	for index, raw := range items {
		link, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("trace[%d] is not an object", index)
		}
		if link["code"] != codes[index] {
			return fmt.Errorf("trace[%d] code = %v, expected %q", index, link["code"], codes[index])
		}
		if link["role"] != roles[index] {
			return fmt.Errorf("trace[%d] role = %v, expected %q", index, link["role"], roles[index])
		}
	}
	return nil
}

func nestedString(root map[string]any, path ...any) (string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", path)
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, ok := root["events"].([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
