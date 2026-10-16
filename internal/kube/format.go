package kube

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/duration"
)

// Value formatting the way kubectl prints it.

const mi = 1024 * 1024

// CPU formats millicores like kubectl top.
func CPU(m int64) string { return fmt.Sprintf("%dm", m) }

// Mem formats bytes like kubectl top, switching to Gi for large values.
func Mem(b int64) string {
	if b >= 10*1024*mi {
		return fmt.Sprintf("%.1fGi", float64(b)/(1024*mi))
	}
	return fmt.Sprintf("%dMi", b/mi)
}

// PctValue returns v as a percentage of of, or -1 when there is no base.
func PctValue(v, of int64) int64 {
	if of <= 0 {
		return -1
	}
	return v * 100 / of
}

// Pct formats PctValue, using "-" when there is no request/limit.
func Pct(v, of int64) string {
	p := PctValue(v, of)
	if p < 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", p)
}

// Age formats a timestamp like kubectl's AGE column.
func Age(t time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	return duration.HumanDuration(time.Since(t))
}
