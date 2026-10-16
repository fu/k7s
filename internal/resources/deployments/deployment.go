// Package deployments is the deployments kind: replicas ready, up to date
// and available, a status light and the images; the change log tells
// rollouts, scaling and why a rollout is stuck.
//
//	deployment.go  a Deployment as k7s sees it
//	resource.go    watch, state, change log, links
//	row.go         what a row shows
package deployments

import (
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

// Row is the flattened view of a Deployment.
type Row struct {
	Namespace, Name                    string
	Desired, Ready, Updated, Available int32
	Images                             []string
	Paused                             bool
	Created                            time.Time
	Secrets                            []string // used by its pod template
	Claims                             []string // volume claims its pod template mounts
	Stuck                              string   // why the rollout cannot progress, if it cannot
	Progress                           string   // the Progressing condition's reason
}

func (d Row) Key() string { return d.Namespace + "/" + d.Name }

func (d Row) ref() resources.Ref {
	return resources.Ref{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name}
}

func (d Row) ReadyString() string { return fmt.Sprintf("%d/%d", d.Ready, d.Desired) }

func fromDeployment(o *appsv1.Deployment) Row {
	d := Row{
		Namespace: o.Namespace,
		Name:      o.Name,
		Desired:   1,
		Ready:     o.Status.ReadyReplicas,
		Updated:   o.Status.UpdatedReplicas,
		Available: o.Status.AvailableReplicas,
		Paused:    o.Spec.Paused,
		Created:   o.CreationTimestamp.Time,
		Secrets:   pods.SecretsUsed(&o.Spec.Template.Spec),
		Claims:    pods.ClaimsUsed(&o.Spec.Template.Spec),
	}
	if o.Spec.Replicas != nil {
		d.Desired = *o.Spec.Replicas
	}
	for _, c := range o.Spec.Template.Spec.Containers {
		d.Images = append(d.Images, c.Image)
	}
	for _, c := range o.Status.Conditions {
		switch {
		case c.Type == appsv1.DeploymentProgressing:
			d.Progress = c.Reason
			if c.Status == corev1.ConditionFalse {
				d.Stuck = c.Reason + ": " + c.Message
			}
		case c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue:
			d.Stuck = c.Reason + ": " + c.Message
		}
	}
	return d
}

// Light: green when every replica is ready and up to date, amber while a
// rollout or scaling is on its way, red when it is stuck, dim at zero.
func (d Row) Light() resources.Tone {
	switch {
	case d.Stuck != "":
		return resources.Alarm
	case d.Desired == 0:
		return resources.Dim
	case d.Ready == d.Desired && d.Updated == d.Desired && d.Available == d.Desired:
		return resources.Good
	}
	return resources.Busy
}

// images is the IMAGES column: images without their registry.
func (d Row) images() string {
	short := make([]string, len(d.Images))
	for i, img := range d.Images {
		if j := strings.LastIndex(img, "/"); j >= 0 {
			img = img[j+1:]
		}
		short[i] = img
	}
	return strings.Join(short, ", ")
}
