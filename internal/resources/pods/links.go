package pods

import (
	"regexp"

	"github.com/fu/k7s/internal/resources"
)

// Links: its controller (a ReplicaSet, a Job, …) and the workload above it
// run the pod; the pod uses its secrets and volume claims.
func (r *Resource) Links() []resources.Link {
	var out []resources.Link
	for _, k := range r.order {
		p := r.rows[k].row
		pod := p.ref()
		if p.OwnerKind != "" {
			own := resources.Ref{Kind: p.OwnerKind, Namespace: p.Namespace, Name: p.OwnerName}
			out = append(out, resources.Link{From: own, To: pod})
			out = append(out, jobOwners(own)...)
		}
		if p.WorkloadKind != "" && p.WorkloadKind != p.OwnerKind {
			wl := resources.Ref{Kind: p.WorkloadKind, Namespace: p.Namespace, Name: p.WorkloadName}
			out = append(out, resources.Link{From: wl, To: pod})
		}
		for _, s := range p.Secrets {
			out = append(out, resources.Link{From: pod, To: resources.Ref{Kind: "Secret", Namespace: p.Namespace, Name: s}})
		}
		for _, c := range p.Claims {
			out = append(out, resources.Link{From: pod, To: resources.Ref{Kind: "PersistentVolumeClaim", Namespace: p.Namespace, Name: c}})
		}
	}
	return out
}

func (p Row) ref() resources.Ref {
	return resources.Ref{Kind: "Pod", Namespace: p.Namespace, Name: p.Name}
}

// Jobs are created by a CronJob ("<name>-<minutes>") or a KEDA ScaledJob
// ("<name>-<5 random>"); the name says which one without asking the API.
var (
	cronJobName   = regexp.MustCompile(`^(.+)-\d{8,}$`)
	scaledJobName = regexp.MustCompile(`^(.+)-[a-z0-9]{5}$`)
)

func jobOwners(job resources.Ref) []resources.Link {
	if job.Kind != "Job" {
		return nil
	}
	var out []resources.Link
	if m := cronJobName.FindStringSubmatch(job.Name); m != nil {
		out = append(out, resources.Link{From: resources.Ref{Kind: "CronJob", Namespace: job.Namespace, Name: m[1]}, To: job})
	}
	if m := scaledJobName.FindStringSubmatch(job.Name); m != nil {
		out = append(out, resources.Link{From: resources.Ref{Kind: "ScaledJob", Namespace: job.Namespace, Name: m[1]}, To: job})
	}
	return out
}
