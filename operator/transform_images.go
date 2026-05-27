package operator

import (
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
)

// TransformImageRefs walks the deployment's container and init-container
// image fields and substitutes any that have a mapping in rewriter. Images
// without a mapping pass through unchanged so non-VICE sidecars or
// not-yet-mirrored images don't block a launch. Returns the number of
// substitutions, so the caller can log when rewriting actually happened.
func TransformImageRefs(deployment *appsv1.Deployment, rewriter ImageRewriter) int {
	if deployment == nil || rewriter == nil {
		return 0
	}
	return rewriteContainerImages(deployment.Spec.Template.Spec.InitContainers, rewriter) +
		rewriteContainerImages(deployment.Spec.Template.Spec.Containers, rewriter)
}

func rewriteContainerImages(containers []apiv1.Container, rewriter ImageRewriter) int {
	count := 0
	for i := range containers {
		if mirrored, ok := rewriter.RewriteImage(containers[i].Image); ok {
			containers[i].Image = mirrored
			count++
		}
	}
	return count
}
