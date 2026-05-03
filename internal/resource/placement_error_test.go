package resource

import "testing"

func TestRunFailureMessageIndicatesPlacementInfeasible(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"resource initialization failed: placement infeasible: cannot place service svc: insufficient host capacity", true},
		{"resource initialization failed: cannot place service svc: insufficient host capacity (each instance needs 1.00 CPU cores, 512.00 MB memory)", true},
		{"resource initialization failed: no hosts defined", true},
		{"run failed: unrelated", false},
	}
	for _, tc := range cases {
		if got := RunFailureMessageIndicatesPlacementInfeasible(tc.msg); got != tc.want {
			t.Fatalf("msg=%q got=%v want=%v", tc.msg, got, tc.want)
		}
	}
}
