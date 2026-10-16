// Package replicasets is the replicasets kind: each rollout revision of a
// Deployment with its desired, current and ready replicas. Old revisions
// (scaled to zero) are dimmed.
//
//	replicaset.go  a ReplicaSet as k7s sees it
//	resource.go    watch, state, change log, links
//	row.go         what a row shows
package replicasets

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/resources"
)

// Row is the flattened view of a ReplicaSet.
type Row struct {
	Namespace, Name         string
	Revision                string // the Deployment's rollout revision
	OwnerKind, OwnerName    string // usually its Deployment
	Desired, Current, Ready int32
	Failure                 string // why it cannot create pods, if it cannot
	Created                 time.Time
}

func (s Row) Key() string { return s.Namespace + "/" + s.Name }

func (s Row) ref() resources.Ref {
	return resources.Ref{Kind: "ReplicaSet", Namespace: s.Namespace, Name: s.Name}
}

func fromReplicaSet(o *appsv1.ReplicaSet) Row {
	s := Row{
		Namespace: o.Namespace,
		Name:      o.Name,
		Revision:  o.Annotations["deployment.kubernetes.io/revision"],
		Desired:   1,
		Current:   o.Status.Replicas,
		Ready:     o.Status.ReadyReplicas,
		Created:   o.CreationTimestamp.Time,
	}
	if o.Spec.Replicas != nil {
		s.Desired = *o.Spec.Replicas
	}
	if c := metav1.GetControllerOf(o); c != nil {
		s.OwnerKind, s.OwnerName = c.Kind, c.Name
	}
	for _, c := range o.Status.Conditions {
		if c.Type == appsv1.ReplicaSetReplicaFailure && c.Status == corev1.ConditionTrue {
			s.Failure = c.Reason + ": " + c.Message
		}
	}
	return s
}

// Light: green when all replicas are ready, amber on the way, red when it
// cannot create pods, dim for an old revision scaled to zero.
func (s Row) Light() resources.Tone {
	switch {
	case s.Failure != "":
		return resources.Alarm
	case s.Desired == 0 && s.Current == 0:
		return resources.Dim
	case s.Ready == s.Desired && s.Current == s.Desired:
		return resources.Good
	}
	return resources.Busy
}
