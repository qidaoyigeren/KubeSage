package service

import "testing"

// TestValidatePodDiagnosisRequestAcceptsRFC1123Names verifies normal
// Kubernetes object names pass validation.
func TestValidatePodDiagnosisRequestAcceptsRFC1123Names(t *testing.T) {
	req := PodDiagnosisRequest{
		Namespace:     "default",
		PodName:       "api-7c9c9d6b5d-abcde",
		ContainerName: "api",
	}
	if err := ValidatePodDiagnosisRequest(req); err != nil {
		t.Fatalf("expected valid request, got %v", err)
	}
}

// TestValidatePodDiagnosisRequestRejectsUnsafeNames verifies user-controlled
// values are blocked before reaching Kubernetes APIs.
func TestValidatePodDiagnosisRequestRejectsUnsafeNames(t *testing.T) {
	cases := []PodDiagnosisRequest{
		{Namespace: "Default", PodName: "api"},
		{Namespace: "default", PodName: "../api"},
		{Namespace: "default", PodName: "api", ContainerName: "bad_name"},
	}
	for _, req := range cases {
		err := ValidatePodDiagnosisRequest(req)
		if !IsInvalidDiagnosisRequest(err) {
			t.Fatalf("expected invalid request for %+v, got %v", req, err)
		}
	}
}
