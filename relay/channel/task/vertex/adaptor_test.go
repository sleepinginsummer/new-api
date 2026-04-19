package vertex

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// TestBuildRequestURLWithAPIKeyProject verifies API Key mode uses project-aware Veo URLs.
func TestBuildRequestURLWithAPIKeyProject(t *testing.T) {
	adaptor := &TaskAdaptor{apiKey: "test-api-key"}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "veo-3.1-generate-001",
			ApiVersion:        "us-central1",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType:   dto.VertexKeyTypeAPIKey,
				VertexProjectID: "lexical-tide-493209-f9",
			},
		},
	}

	url, err := adaptor.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("BuildRequestURL returned error: %v", err)
	}

	expected := "https://us-central1-aiplatform.googleapis.com/v1/projects/lexical-tide-493209-f9/locations/us-central1/publishers/google/models/veo-3.1-generate-001:predictLongRunning?key=test-api-key"
	if url != expected {
		t.Fatalf("unexpected url\nwant: %s\ngot:  %s", expected, url)
	}
}

// TestBuildRequestURLWithAPIKeyMissingProject verifies missing project_id is rejected early.
func TestBuildRequestURLWithAPIKeyMissingProject(t *testing.T) {
	adaptor := &TaskAdaptor{apiKey: "test-api-key"}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "veo-3.1-generate-001",
			ApiVersion:        "us-central1",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeAPIKey,
			},
		},
	}

	_, err := adaptor.BuildRequestURL(info)
	if err == nil || !strings.Contains(err.Error(), "vertex_project_id") {
		t.Fatalf("expected vertex_project_id error, got: %v", err)
	}
}

// TestBuildRequestURLWithJSONKey verifies JSON service account mode keeps the old project-aware URL format.
func TestBuildRequestURLWithJSONKey(t *testing.T) {
	adaptor := &TaskAdaptor{apiKey: `{"project_id":"lexical-tide-493209-f9"}`}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "veo-3.0-generate-001",
			ApiVersion:        "us-central1",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				VertexKeyType: dto.VertexKeyTypeJSON,
			},
		},
	}

	url, err := adaptor.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("BuildRequestURL returned error: %v", err)
	}

	expected := "https://us-central1-aiplatform.googleapis.com/v1/projects/lexical-tide-493209-f9/locations/us-central1/publishers/google/models/veo-3.0-generate-001:predictLongRunning"
	if url != expected {
		t.Fatalf("unexpected url\nwant: %s\ngot:  %s", expected, url)
	}
}

// TestBuildVertexProjectURL verifies fetch/submit project URLs share the same builder.
func TestBuildVertexProjectURL(t *testing.T) {
	url := buildVertexProjectURL("lexical-tide-493209-f9", "us-central1", "veo-3.1-generate-001", "fetchPredictOperation")
	expected := "https://us-central1-aiplatform.googleapis.com/v1/projects/lexical-tide-493209-f9/locations/us-central1/publishers/google/models/veo-3.1-generate-001:fetchPredictOperation"
	if url != expected {
		t.Fatalf("unexpected url\nwant: %s\ngot:  %s", expected, url)
	}
}

// TestFetchTaskUsesProjectScopedURL verifies task polling keeps the upstream project/region path when using API key mode.
func TestFetchTaskUsesProjectScopedURL(t *testing.T) {
	upstream := "projects/lexical-tide-493209-f9/locations/us-central1/publishers/google/models/veo-3.1-generate-001/operations/test-operation"
	taskID := taskcommon.EncodeLocalTaskID(upstream)

	project := extractProjectFromOperationName(upstream)
	region := extractRegionFromOperationName(upstream)
	modelName := extractModelFromOperationName(upstream)
	url := buildVertexProjectURL(project, region, modelName, "fetchPredictOperation")
	url = appendAPIKeyToURL(url, "test-api-key")

	expected := "https://us-central1-aiplatform.googleapis.com/v1/projects/lexical-tide-493209-f9/locations/us-central1/publishers/google/models/veo-3.1-generate-001:fetchPredictOperation?key=test-api-key"
	if url != expected {
		t.Fatalf("unexpected poll url for task %s\nwant: %s\ngot:  %s", taskID, expected, url)
	}
}
