package operator

import (
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
)

// TransformImageRefs walks the deployment's container and init-container
// image fields and substitutes any that have a mapping in rewriter. Images
// without a mapping pass through unchanged so non-VICE sidecars or
// not-yet-mirrored images don't block a launch. Always emits an info log
// summarizing the pass: an admin can grep for "image-ref rewrite" to see
// whether manual-mirror engaged for a given launch, including the
// drift-warning case where the rewriter is configured but matched
// nothing.
func TransformImageRefs(deployment *appsv1.Deployment, rewriter ImageRewriter) {
	if deployment == nil || rewriter == nil {
		return
	}
	n := rewriteContainerImages(deployment.Spec.Template.Spec.InitContainers, rewriter) +
		rewriteContainerImages(deployment.Spec.Template.Spec.Containers, rewriter)
	log.Infof("image-ref rewrite: %d substitution(s) in deployment %s", n, deployment.Name)
}

func rewriteContainerImages(containers []apiv1.Container, rewriter ImageRewriter) int {
	var count int
	for i := range containers {
		if mirrored, ok := rewriter.RewriteImage(containers[i].Image); ok {
			containers[i].Image = mirrored
			count++
		}
	}
	return count
}
