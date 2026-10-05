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
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
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
		"calibration-chain":        checkCalibrationChain,
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

	rootID, err := h.createStandard(" std-root-a ", "")
	if err != nil {
		return err
	}

	read, err := h.request(http.MethodGet, "/standards/"+rootID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	code, err := nestedString(read.Body, "standard", "code")
	if err != nil {
		return err
	}
	if code != "STD-ROOT-A" {
		return fmt.Errorf("standard code was not normalized: %q", code)
	}
	status, err := nestedString(read.Body, "standard", "status")
	if err != nil {
		return err
	}
	if status != "active" {
		return fmt.Errorf("registered standard status = %q", status)
	}
	if _, ok := nestedMap(read.Body, "standard")["superior_id"]; ok {
		return errors.New("root standard must not carry a superior reference")
	}

	duplicate, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":        "STD-ROOT-A",
		"valid_from":  "2020-01-01T00:00:00Z",
		"valid_until": "2099-01-01T00:00:00Z",
		"steward":     "Lin",
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

	invalid, err := h.request(http.MethodPost, "/standards", map[string]any{
		"code":        "STD-BAD-INTERVAL",
		"valid_from":  "2099-01-01T00:00:00Z",
		"valid_until": "2020-01-01T00:00:00Z",
		"steward":     "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(invalid, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(invalid, "invalid_input"); err != nil {
		return err
	}

	childID, err := h.createStandard("STD-CHILD-A", rootID)
	if err != nil {
		return err
	}
	child, err := h.request(http.MethodGet, "/standards/"+childID, nil)
	if err != nil {
		return err
	}
	superior, err := nestedString(child.Body, "standard", "superior_id")
	if err != nil {
		return err
	}
	if superior != rootID {
		return fmt.Errorf("child superior = %q, expected %q", superior, rootID)
	}

	deactivated, err := h.request(http.MethodPost, "/standards/"+childID+"/deactivate", map[string]any{
		"actor":  "Lin",
		"reason": "reference resistor drifted beyond repair",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(deactivated, http.StatusOK); err != nil {
		return err
	}
	deactivatedStatus, err := nestedString(deactivated.Body, "standard", "status")
	if err != nil {
		return err
	}
	if deactivatedStatus != "deactivated" {
		return fmt.Errorf("deactivated standard status = %q", deactivatedStatus)
	}
	if _, err := nestedString(deactivated.Body, "standard", "deactivated_at"); err != nil {
		return err
	}

	again, err := h.request(http.MethodPost, "/standards/"+childID+"/deactivate", map[string]any{
		"actor": "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(again, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(again, "state_conflict"); err != nil {
		return err
	}

	amendDeactivated, err := h.request(http.MethodPost, "/standards/"+childID+"/amend", map[string]any{
		"superior_id": rootID,
		"valid_from":  "2020-01-01T00:00:00Z",
		"valid_until": "2099-01-01T00:00:00Z",
		"actor":       "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(amendDeactivated, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(amendDeactivated, "state_conflict"); err != nil {
		return err
	}

	amended, err := h.request(http.MethodPost, "/standards/"+rootID+"/amend", map[string]any{
		"superior_id": "",
		"valid_from":  "2021-01-01T00:00:00Z",
		"valid_until": "2098-01-01T00:00:00Z",
		"actor":       "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(amended, http.StatusOK); err != nil {
		return err
	}
	validUntil, err := nestedString(amended.Body, "standard", "valid_until")
	if err != nil {
		return err
	}
	if validUntil != "2098-01-01T00:00:00Z" {
		return fmt.Errorf("amended valid_until = %q", validUntil)
	}

	missing, err := h.request(http.MethodGet, "/standards/standard-0000", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(missing, http.StatusNotFound); err != nil {
		return err
	}
	return expectErrorCode(missing, "not_found")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	standardID, err := h.createStandard("STD-OPEN-1", "")
	if err != nil {
		return err
	}
	deviceID, err := h.createDevice("MET-1101")
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
	basisStandard, err := nestedString(opened.Body, "case", "basis", "standard_id")
	if err != nil {
		return err
	}
	if basisStandard != standardID {
		return fmt.Errorf("case basis standard = %q, expected %q", basisStandard, standardID)
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
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	standardID, err := h.createStandard("STD-MEAS-1", "")
	if err != nil {
		return err
	}
	deviceID, err := h.createDevice("MET-1201")
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
	rejectCase, err := h.openCase(rejectDeviceID, standardID)
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

	standardID, err := h.createStandard("STD-REOPEN-1", "")
	if err != nil {
		return err
	}
	caseID, err := h.rejectedCase("MET-1301", standardID)
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

	standardID, err := h.createStandard("STD-SEAL-1", "")
	if err != nil {
		return err
	}
	caseID, err := h.readyCase("MET-1401", standardID)
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

func checkCalibrationChain() error {
	h := newHarness()
	defer h.close()

	// A three-level chain resolves onto the case basis at open time.
	rootID, err := h.createStandard("STD-CHAIN-ROOT", "")
	if err != nil {
		return err
	}
	midID, err := h.createStandard("STD-CHAIN-MID", rootID)
	if err != nil {
		return err
	}
	workingID, err := h.createStandard("STD-CHAIN-WORK", midID)
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
	if err := expectChain(opened.Body, []any{"case", "basis", "links"}, workingID, midID, rootID); err != nil {
		return err
	}

	// Measurement and seal succeed while the chain is usable, and the seal
	// records the chain verified at seal time.
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Wei",
		"summary":  "chain verified to the root basis",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	if err := expectChain(sealed.Body, []any{"seal", "chain"}, workingID, midID, rootID); err != nil {
		return err
	}

	// Deactivating the root after sealing must not rewrite the sealed facts.
	deactivated, err := h.request(http.MethodPost, "/standards/"+rootID+"/deactivate", map[string]any{
		"actor":  "Lin",
		"reason": "root reference retired from service",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(deactivated, http.StatusOK); err != nil {
		return err
	}
	historical, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(historical, http.StatusOK); err != nil {
		return err
	}
	historicalState, err := nestedString(historical.Body, "case", "state")
	if err != nil {
		return err
	}
	if historicalState != "sealed" {
		return fmt.Errorf("historical case state = %q", historicalState)
	}
	if err := expectChain(historical.Body, []any{"case", "basis", "links"}, workingID, midID, rootID); err != nil {
		return err
	}
	if err := expectChain(historical.Body, []any{"seal", "chain"}, workingID, midID, rootID); err != nil {
		return err
	}

	// A deactivated root blocks new cases on the same chain.
	blockedDevice, err := h.createDevice("MET-1502")
	if err != nil {
		return err
	}
	blockedOpen, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   blockedDevice,
		"standard_id": workingID,
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
	if err := expectErrorCode(blockedOpen, "chain_broken"); err != nil {
		return err
	}

	// A standard that fails after a case opened blocks new measurements.
	expRootID, err := h.createStandard("STD-CHAIN-EXP-ROOT", "")
	if err != nil {
		return err
	}
	expWorkID, err := h.createStandard("STD-CHAIN-EXP-WORK", expRootID)
	if err != nil {
		return err
	}
	expDevice, err := h.createDevice("MET-1503")
	if err != nil {
		return err
	}
	expCase, err := h.openCase(expDevice, expWorkID)
	if err != nil {
		return err
	}
	expCaseID, err := nestedString(expCase.Body, "case", "id")
	if err != nil {
		return err
	}
	if _, err := h.request(http.MethodPost, "/standards/"+expRootID+"/deactivate", map[string]any{
		"actor": "Lin",
	}); err != nil {
		return err
	}
	blockedMeasure, err := h.recordMeasurement(expCaseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(blockedMeasure, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedMeasure, "chain_broken"); err != nil {
		return err
	}

	// A standard that fails after an accepted measurement blocks sealing.
	sealRootID, err := h.createStandard("STD-CHAIN-SEAL-ROOT", "")
	if err != nil {
		return err
	}
	sealWorkID, err := h.createStandard("STD-CHAIN-SEAL-WORK", sealRootID)
	if err != nil {
		return err
	}
	sealDevice, err := h.createDevice("MET-1504")
	if err != nil {
		return err
	}
	sealCase, err := h.openCase(sealDevice, sealWorkID)
	if err != nil {
		return err
	}
	sealCaseID, err := nestedString(sealCase.Body, "case", "id")
	if err != nil {
		return err
	}
	ready, err := h.recordMeasurement(sealCaseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(ready, http.StatusCreated); err != nil {
		return err
	}
	if _, err := h.request(http.MethodPost, "/standards/"+sealWorkID+"/deactivate", map[string]any{
		"actor": "Lin",
	}); err != nil {
		return err
	}
	blockedSeal, err := h.request(http.MethodPost, "/cases/"+sealCaseID+"/seal", map[string]any{
		"reviewer": "Wei",
		"summary":  "attempted seal after the working standard was deactivated",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedSeal, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blockedSeal, "chain_broken"); err != nil {
		return err
	}

	// A cycle introduced by a later amendment breaks the chain for new work
	// while the formed case keeps its original basis snapshot.
	cycleRootID, err := h.createStandard("STD-CHAIN-CYC-ROOT", "")
	if err != nil {
		return err
	}
	cycleWorkID, err := h.createStandard("STD-CHAIN-CYC-WORK", cycleRootID)
	if err != nil {
		return err
	}
	cycleDevice, err := h.createDevice("MET-1505")
	if err != nil {
		return err
	}
	cycleCase, err := h.openCase(cycleDevice, cycleWorkID)
	if err != nil {
		return err
	}
	cycleCaseID, err := nestedString(cycleCase.Body, "case", "id")
	if err != nil {
		return err
	}
	amended, err := h.request(http.MethodPost, "/standards/"+cycleRootID+"/amend", map[string]any{
		"superior_id": cycleWorkID,
		"valid_from":  "2020-01-01T00:00:00Z",
		"valid_until": "2099-01-01T00:00:00Z",
		"actor":       "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(amended, http.StatusOK); err != nil {
		return err
	}
	cycleMeasure, err := h.recordMeasurement(cycleCaseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(cycleMeasure, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(cycleMeasure, "chain_broken"); err != nil {
		return err
	}
	cycleDevice2, err := h.createDevice("MET-1506")
	if err != nil {
		return err
	}
	cycleOpen, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   cycleDevice2,
		"standard_id": cycleWorkID,
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(cycleOpen, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(cycleOpen, "chain_broken"); err != nil {
		return err
	}
	cycleHistorical, err := h.request(http.MethodGet, "/cases/"+cycleCaseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(cycleHistorical, http.StatusOK); err != nil {
		return err
	}
	if err := expectChain(cycleHistorical.Body, []any{"case", "basis", "links"}, cycleWorkID, cycleRootID); err != nil {
		return err
	}

	// An amendment that expires the validity interval blocks new measurements
	// but does not rewrite the validity recorded on the formed case.
	expireRootID, err := h.createStandard("STD-CHAIN-VAL-ROOT", "")
	if err != nil {
		return err
	}
	expireWorkID, err := h.createStandard("STD-CHAIN-VAL-WORK", expireRootID)
	if err != nil {
		return err
	}
	expireDevice, err := h.createDevice("MET-1507")
	if err != nil {
		return err
	}
	expireCase, err := h.openCase(expireDevice, expireWorkID)
	if err != nil {
		return err
	}
	expireCaseID, err := nestedString(expireCase.Body, "case", "id")
	if err != nil {
		return err
	}
	expired, err := h.request(http.MethodPost, "/standards/"+expireWorkID+"/amend", map[string]any{
		"superior_id": expireRootID,
		"valid_from":  "2020-01-01T00:00:00Z",
		"valid_until": "2021-01-01T00:00:00Z",
		"actor":       "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(expired, http.StatusOK); err != nil {
		return err
	}
	expiredMeasure, err := h.recordMeasurement(expireCaseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(expiredMeasure, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(expiredMeasure, "chain_broken"); err != nil {
		return err
	}
	expireHistorical, err := h.request(http.MethodGet, "/cases/"+expireCaseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(expireHistorical, http.StatusOK); err != nil {
		return err
	}
	recordedUntil, err := nestedAny(expireHistorical.Body, "case", "basis", "links", 0, "valid_until")
	if err != nil {
		return err
	}
	if recordedUntil != "2099-01-01T00:00:00Z" {
		return fmt.Errorf("formed case basis was rewritten: valid_until = %v", recordedUntil)
	}

	// A chain with an unregistered reference is incomplete and cannot be used.
	danglingID, err := h.createStandard("STD-CHAIN-DANGLING", "standard-9999")
	if err != nil {
		return err
	}
	danglingDevice, err := h.createDevice("MET-1508")
	if err != nil {
		return err
	}
	danglingOpen, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   danglingDevice,
		"standard_id": danglingID,
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(danglingOpen, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(danglingOpen, "chain_broken"); err != nil {
		return err
	}

	// An unknown standard reference is a not-found, not a chain failure.
	unknownDevice, err := h.createDevice("MET-1509")
	if err != nil {
		return err
	}
	unknownOpen, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id":   unknownDevice,
		"standard_id": "standard-0000",
		"nominal":     25.0,
		"tolerance":   0.5,
		"steward":     "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(unknownOpen, http.StatusNotFound); err != nil {
		return err
	}
	return expectErrorCode(unknownOpen, "not_found")
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

func (h *harness) createStandard(code, superiorID string) (string, error) {
	body := map[string]any{
		"code":        code,
		"valid_from":  "2020-01-01T00:00:00Z",
		"valid_until": "2099-01-01T00:00:00Z",
		"steward":     "Lin",
	}
	if superiorID != "" {
		body["superior_id"] = superiorID
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

func (h *harness) rejectedCase(tag, standardID string) (string, error) {
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
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag, standardID string) (string, error) {
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

// expectChain verifies that the links at the given path resolve exactly the
// expected standard sequence and terminate at a root basis.
func expectChain(root map[string]any, path []any, expected ...string) error {
	linksValue, err := nestedAny(root, path...)
	if err != nil {
		return err
	}
	links, ok := linksValue.([]any)
	if !ok {
		return fmt.Errorf("%v is not an array", path)
	}
	if len(links) != len(expected) {
		return fmt.Errorf("chain length = %d, expected %d: %v", len(links), len(expected), linksValue)
	}
	for index, want := range expected {
		link, ok := links[index].(map[string]any)
		if !ok {
			return fmt.Errorf("chain link %d is not an object", index)
		}
		if link["standard_id"] != want {
			return fmt.Errorf("chain link %d standard_id = %v, expected %q", index, link["standard_id"], want)
		}
		if index+1 < len(expected) {
			if link["superior_id"] != expected[index+1] {
				return fmt.Errorf("chain link %d superior_id = %v, expected %q", index, link["superior_id"], expected[index+1])
			}
		} else if _, ok := link["superior_id"]; ok {
			return fmt.Errorf("chain root link %d must not carry a superior reference", index)
		}
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedMap(root map[string]any, path ...string) map[string]any {
	value, err := nestedValue(root, path...)
	if err != nil {
		return map[string]any{}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return object
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
	items, err := nestedSlice(root, "events")
	if err != nil {
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

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
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
