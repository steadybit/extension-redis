package extpreflight

import (
	"context"
	"errors"

	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/preflight-kit/go/preflight_kit_api"
	"github.com/steadybit/preflight-kit/go/preflight_kit_sdk/v2"
)

// MaintenanceWindowPreflight actions if experiments run within allowed time windows
type MaintenanceWindowPreflight struct {
}

type MaintenanceWindowPreflightState struct {
	StatusCount   int
	ExecutionName *string
}

// NewMaintenanceWindowPreflight creates a new maintenance window preflight
func NewMaintenanceWindowPreflight() *MaintenanceWindowPreflight {
	return &MaintenanceWindowPreflight{}
}

// Make sure action implements all required interfaces
var (
	_ preflight_kit_sdk.Preflight[MaintenanceWindowPreflightState] = (*MaintenanceWindowPreflight)(nil)
)

// Describe returns the preflight description
func (preflight *MaintenanceWindowPreflight) Describe() preflight_kit_api.PreflightDescription {
	return preflight_kit_api.PreflightDescription{
		Id:                      "com.example.preflights.maintenance-window",
		Version:                 "v0.1.0",
		Label:                   "Maintenance Window Check",
		Description:             "Ensures experiments only run during specified maintenance windows",
		Icon:                    extutil.Ptr("data:image/svg+xml;base64,PHN2ZyB3aWR0aD0iMjQiIGhlaWdodD0iMjQiIHZpZXdCb3g9IjAgMCAyNCAyNCIgZmlsbD0ibm9uZSIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj4KPHBhdGggZD0iTTEyIDIxQzE2Ljk3MDYgMjEgMjEgMTYuOTcwNiAyMSAxMkMyMSA3LjAyOTQ0IDE2Ljk3MDYgMyAxMiAzQzcuMDI5NDQgMyAzIDcuMDI5NDQgMyAxMkMzIDE2Ljk3MDYgNy4wMjk0NCAyMSAxMiAyMVoiIHN0cm9rZT0iIzE4MTgxOCIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiLz4KPHBhdGggZD0iTTEyIDYuNzVWMTJINi43NjgwMSIgc3Ryb2tlPSIjMTgxODE4IiBzdHJva2Utd2lkdGg9IjIiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCIvPgo8L3N2Zz4K"),
		TargetAttributeIncludes: []string{"host.hostname", "k8s.deployment"},
		// Define endpoint references for the preflight lifecycle
		Start: preflight_kit_api.MutatingEndpointReference{},
		Status: preflight_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("1s"), // Status will be checked every 1 second
		},
		Cancel: &preflight_kit_api.MutatingEndpointReference{},
	}
}

func (preflight *MaintenanceWindowPreflight) NewEmptyState() MaintenanceWindowPreflightState {
	return MaintenanceWindowPreflightState{}
}

// Start initiates the preflight action
func (preflight *MaintenanceWindowPreflight) Start(_ context.Context, state *MaintenanceWindowPreflightState, request preflight_kit_api.StartPreflightRequestBody) (*preflight_kit_api.StartResult, error) {
	// Store the experiment name in the state
	state.ExecutionName = request.ExperimentExecution.Name

	// Example of how to return an error if needed
	if request.ExperimentExecution.Name != nil && *request.ExperimentExecution.Name == "TechnicalError" {
		return nil, extutil.Ptr(extension_kit.ToError("Technical error during preflight start", errors.New("detailed error info")))
	}

	// Example of returning a failure
	if request.ExperimentExecution.Name != nil && *request.ExperimentExecution.Name == "StartFailure" {
		return &preflight_kit_api.StartResult{
			Error: extutil.Ptr(preflight_kit_api.PreflightKitError{
				Title:  "Preflight start failure",
				Status: extutil.Ptr(preflight_kit_api.Failed),
			}),
		}, nil
	}

	// Normal successful start
	return &preflight_kit_api.StartResult{}, nil
}

// Status checks the current status of the preflight
func (preflight *MaintenanceWindowPreflight) Status(_ context.Context, state *MaintenanceWindowPreflightState) (*preflight_kit_api.StatusResult, error) {
	// Increment the status counter for this preflight
	state.StatusCount = state.StatusCount + 1

	// Return not completed for the first few calls to simulate a longer-running check
	if state.StatusCount < 2 {
		return &preflight_kit_api.StatusResult{Completed: false}, nil
	}

	// Example check logic - in a real implementation, you would check
	// if the current time is within the allowed maintenance window
	if state.ExecutionName != nil && *state.ExecutionName == "OutsideMaintenanceWindow" {
		return &preflight_kit_api.StatusResult{
			Completed: true,
			Error: &preflight_kit_api.PreflightKitError{
				Title:  "Outside maintenance window",
				Detail: extutil.Ptr("Experiment is scheduled outside the allowed maintenance window (8 PM to 6 AM UTC)"),
				Status: extutil.Ptr(preflight_kit_api.Failed),
			},
		}, nil
	}

	// If all checks pass, return completed with no error
	return &preflight_kit_api.StatusResult{Completed: true}, nil
}
