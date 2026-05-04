package simd

import (
	"fmt"
	"testing"
)

func TestOnlineControllerPolicyBranchFromPrimary(t *testing.T) {
	cases := []struct {
		in   string
		want onlineControllerPolicyBranch
	}{
		{"cpu_utilization", onlinePolicyCPU},
		{" CPU_UTILIZATION ", onlinePolicyCPU},
		{"memory_utilization", onlinePolicyMemory},
		{"memory_utilization ", onlinePolicyMemory},
		{"", onlinePolicyP95},
		{"p95_latency", onlinePolicyP95},
		{"unknown_objective", onlinePolicyP95},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			t.Parallel()
			if got := onlineControllerPolicyBranchFromPrimary(tc.in); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
