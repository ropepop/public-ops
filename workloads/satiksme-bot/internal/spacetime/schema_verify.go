package spacetime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SchemaTarget struct {
	Host     string
	Database string
}

type SchemaInfo struct {
	Module        string `json:"module"`
	SchemaVersion string `json:"schemaVersion"`
}

func VerifyExpectedSchema(ctx context.Context, client *http.Client, target SchemaTarget) error {
	target, err := normalizeSchemaTarget(target)
	if err != nil {
		return err
	}
	info, err := fetchSchemaInfo(ctx, client, target)
	if err != nil {
		return err
	}
	if info.Module != ExpectedSchemaModule || info.SchemaVersion != ExpectedSchemaVersion {
		return fmt.Errorf(
			"spacetime schema mismatch for host %s database %s: expected module=%q schemaVersion=%q, got module=%q schemaVersion=%q",
			target.Host,
			target.Database,
			ExpectedSchemaModule,
			ExpectedSchemaVersion,
			info.Module,
			info.SchemaVersion,
		)
	}
	return nil
}

func normalizeSchemaTarget(target SchemaTarget) (SchemaTarget, error) {
	target.Host = strings.TrimRight(strings.TrimSpace(target.Host), "/")
	target.Database = strings.TrimSpace(target.Database)
	if target.Host == "" {
		return SchemaTarget{}, fmt.Errorf("spacetime schema check host is required")
	}
	if target.Database == "" {
		return SchemaTarget{}, fmt.Errorf("spacetime schema check database is required")
	}
	return target, nil
}

func fetchSchemaInfo(ctx context.Context, client *http.Client, target SchemaTarget) (SchemaInfo, error) {
	httpClient := client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	requestURL := fmt.Sprintf(
		"%s/v1/database/%s/call/%s",
		target.Host,
		url.PathEscape(target.Database),
		url.PathEscape(schemaInfoProcedureName),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewBufferString("[]"))
	if err != nil {
		return SchemaInfo{}, fmt.Errorf("build spacetime schema request for host %s database %s: %w", target.Host, target.Database, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return SchemaInfo{}, fmt.Errorf("call spacetime schema info for host %s database %s: %w", target.Host, target.Database, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return SchemaInfo{}, fmt.Errorf("read spacetime schema info for host %s database %s: %w", target.Host, target.Database, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body := strings.TrimSpace(string(responseBody))
		if missingProcedureResponse(resp.StatusCode, responseBody) {
			return SchemaInfo{}, fmt.Errorf(
				"spacetime schema info procedure %q is missing for host %s database %s (expected schema version %q): %s",
				schemaInfoProcedureName,
				target.Host,
				target.Database,
				ExpectedSchemaVersion,
				body,
			)
		}
		return SchemaInfo{}, fmt.Errorf(
			"spacetime schema info request failed for host %s database %s with HTTP %d: %s",
			target.Host,
			target.Database,
			resp.StatusCode,
			body,
		)
	}

	payload, err := decodeProcedureResponseBody(responseBody)
	if err != nil {
		return SchemaInfo{}, fmt.Errorf("decode spacetime schema info for host %s database %s: %w", target.Host, target.Database, err)
	}
	var info SchemaInfo
	if err := decodeInto(payload, &info); err != nil {
		return SchemaInfo{}, fmt.Errorf("decode spacetime schema info payload for host %s database %s: %w", target.Host, target.Database, err)
	}
	info.Module = strings.TrimSpace(info.Module)
	info.SchemaVersion = strings.TrimSpace(info.SchemaVersion)
	if info.Module == "" || info.SchemaVersion == "" {
		rawPayload, _ := json.Marshal(payload)
		return SchemaInfo{}, fmt.Errorf(
			"decode spacetime schema info payload for host %s database %s: missing module or schemaVersion in %s",
			target.Host,
			target.Database,
			strings.TrimSpace(string(rawPayload)),
		)
	}
	return info, nil
}
