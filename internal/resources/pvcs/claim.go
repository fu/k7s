// Package pvcs is the claims kind: the PersistentVolumeClaims of the
// namespace — bound or not, to which volume, how big, how they can be
// mounted. Pods (and their Deployments) link to the claims they mount, a
// claim to its volume.
//
//	claim.go     a PersistentVolumeClaim as k7s sees it
//	resource.go  watch, state, change log, links
//	row.go       what a row shows
package pvcs

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/fu/k7s/internal/resources"
)

// Row is the flattened view of a PersistentVolumeClaim.
type Row struct {
	Namespace, Name string
	Phase           string // Pending, Bound, Lost
	Volume          string // the PersistentVolume it is bound to
	Capacity        string // what it got, e.g. 10Gi (or what it asks for)
	Access          string // e.g. RWO, RWX
	Class           string // storage class
	Resizing        bool
	Created         time.Time
}

func (c Row) Key() string { return c.Namespace + "/" + c.Name }

func (c Row) ref() resources.Ref {
	return resources.Ref{Kind: "PersistentVolumeClaim", Namespace: c.Namespace, Name: c.Name}
}

func fromClaim(o *corev1.PersistentVolumeClaim) Row {
	c := Row{
		Namespace: o.Namespace,
		Name:      o.Name,
		Phase:     string(o.Status.Phase),
		Volume:    o.Spec.VolumeName,
		Created:   o.CreationTimestamp.Time,
	}
	if q, ok := o.Status.Capacity[corev1.ResourceStorage]; ok {
		c.Capacity = q.String()
	} else if q, ok := o.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		c.Capacity = q.String()
	}
	modes := o.Status.AccessModes
	if len(modes) == 0 {
		modes = o.Spec.AccessModes
	}
	c.Access = accessModes(modes)
	if o.Spec.StorageClassName != nil {
		c.Class = *o.Spec.StorageClassName
	}
	for _, cond := range o.Status.Conditions {
		if (cond.Type == corev1.PersistentVolumeClaimResizing || cond.Type == corev1.PersistentVolumeClaimFileSystemResizePending) &&
			cond.Status == corev1.ConditionTrue {
			c.Resizing = true
		}
	}
	return c
}

// accessModes abbreviates like kubectl: RWO, ROX, RWX, RWOP.
func accessModes(modes []corev1.PersistentVolumeAccessMode) string {
	short := map[corev1.PersistentVolumeAccessMode]string{
		corev1.ReadWriteOnce: "RWO", corev1.ReadOnlyMany: "ROX",
		corev1.ReadWriteMany: "RWX", corev1.ReadWriteOncePod: "RWOP",
	}
	var out []string
	for _, m := range modes {
		out = append(out, short[m])
	}
	return strings.Join(out, ",")
}

// Light: green bound, amber pending or resizing, red lost.
func (c Row) Light() resources.Tone {
	switch {
	case c.Phase == string(corev1.ClaimLost):
		return resources.Alarm
	case c.Phase == string(corev1.ClaimBound) && !c.Resizing:
		return resources.Good
	}
	return resources.Busy
}

// state is the row's state for hiding rows.
func (c Row) state() string { return strings.ToLower(c.Phase) }
