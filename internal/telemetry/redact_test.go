package telemetry

import "testing"

func TestRedactLogValue(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: "delivery failed for device_id=edge-1",
			want:  "delivery failed for device_id=<redacted>",
		},
		{
			input: "command_id: 550e8400-e29b-41d4-a716-446655440000 stale",
			want:  "command_id=<redacted> stale",
		},
		{
			input: "scheduler cycle complete",
			want:  "scheduler cycle complete",
		},
	}
	for _, testCase := range cases {
		if got := RedactLogValue(testCase.input); got != testCase.want {
			t.Fatalf("RedactLogValue(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}
