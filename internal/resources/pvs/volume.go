// Package pvs is the volumes kind: the PersistentVolumes bound to claims of
// the namespace (PersistentVolumes themselves belong to the cluster). With
// a filter, only those of the listed claims.
//
//	volume.go    a PersistentVolume as k7s sees it
//	resource.go  watch, state, change log, links
//	row.go       what a row shows
package pvs

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/fu/k7s/internal/resources"
)

// Row is the flattened view of a PersistentVolume.
type Row struct {
	Name                  string
	Phase                 string // Available, Bound, Released, Failed, Pending
	ClaimNamespace, Claim string // the claim it is (or was) bound to
	Capacity              string
	Reclaim               string // Delete, Retain, Recycle
	Class                 string
	Reason                string // why it failed, if it did
	Created               time.Time
}

func (v Row) ref() resources.Ref { return resources.Ref{Kind: "PersistentVolume", Name: v.Name} }

func (v Row) claimRef() resources.Ref {
	return resources.Ref{Kind: "PersistentVolumeClaim", Namespace: v.ClaimNamespace, Name: v.Claim}
}

func fromVolume(o *corev1.PersistentVolume) Row {
	v := Row{
		Name:    o.Name,
		Phase:   string(o.Status.Phase),
		Reclaim: string(o.Spec.PersistentVolumeReclaimPolicy),
		Class:   o.Spec.StorageClassName,
		Reason:  strings.TrimSpace(o.Status.Reason + " " + o.Status.Message),
		Created: o.CreationTimestamp.Time,
	}
	if c := o.Spec.ClaimRef; c != nil {
		v.ClaimNamespace, v.Claim = c.Namespace, c.Name
	}
	if q, ok := o.Spec.Capacity[corev1.ResourceStorage]; ok {
		v.Capacity = q.String()
	}
	return v
}

// Light: green bound, amber available or pending, yellow released (its
// claim is gone, the data may still be there), red failed.
func (v Row) Light() resources.Tone {
	switch corev1.PersistentVolumePhase(v.Phase) {
	case corev1.VolumeBound:
		return resources.Good
	case corev1.VolumeReleased:
		return resources.Warn
	case corev1.VolumeFailed:
		return resources.Alarm
	}
	return resources.Busy
}

func (v Row) state() string { return strings.ToLower(v.Phase) }
