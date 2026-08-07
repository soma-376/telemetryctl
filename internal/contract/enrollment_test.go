package contract

import "testing"

func TestParseEnrollmentRequiresTelemetryToken(t *testing.T) {
	raw := []byte(`{
      "installation_id":"inst_123",
      "installation_token":"pit_secret",
      "manifest":{
        "schema_version":1,
        "config_revision":1,
        "otlp":{"endpoint":"https://collector.example.com","protocol":"grpc"},
        "signals":{"logs":true,"metrics":true,"traces":true},
        "privacy":{
          "collect_user_prompts":false,
          "collect_assistant_responses":false,
          "collect_tool_details":false,
          "collect_tool_content":false,
          "collect_user_email":false,
          "collect_raw_api_bodies":false
        }
      }
    }`)

	if _, err := ParseEnrollment(raw); err == nil {
		t.Fatal("expected missing telemetry_token to fail validation")
	}
}
