package batch

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/argoproj/argo-workflows/v3/cmd/argo/commands/client"
	workflowpkg "github.com/argoproj/argo-workflows/v3/pkg/apiclient/workflow"
	v1alpha1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/cyverse-de/app-exposer/imageinfo"
	"github.com/cyverse-de/app-exposer/resourcing"
	"github.com/cyverse-de/model/v7"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

var (
	defaultStorageCapacity, _ = resourcev1.ParseQuantity("5Gi")
	defaultVolumeName         = "workdir"
	statusRunning             = "running"
)

type BatchSubmissionOpts struct {
	FileTransferImage      string
	FileTransferLogLevel   string
	FileTransferWorkingDir string
	StatusSenderImage      string
	ExternalID             string
}

type WorkflowMaker struct {
	getter   imageinfo.InfoGetter
	analysis *model.Analysis
}

// NewWorkflowMaker creates a new instance of WorkflowMaker
func NewWorkflowMaker(getter imageinfo.InfoGetter, analysis *model.Analysis) *WorkflowMaker {
	return &WorkflowMaker{
		getter:   getter,
		analysis: analysis,
	}
}

// stepTemplates creates a list of templates based on the steps
// defined in the analysis description.
func (w *WorkflowMaker) stepTemplates() ([]v1alpha1.Template, error) {
	var templates []v1alpha1.Template

	for idx, step := range w.analysis.Steps {
		var (
			sourceParts []string
			source      string
		)

		// If there's an entrypoint defined, it needs to be the command in the script.
		if step.Component.Container.EntryPoint != "" {
			sourceParts = append(sourceParts, step.Component.Container.EntryPoint)
		} else {
			// It's possible that the integrator didn't bother to include the entrypoint
			// in the definition, causing us to depend on the entrypoint defined in the
			// image. We need to get the entrypoint in the image (if it exists), so that
			// we can include it in the generated script that the image will run.
			if w.getter.IsInRepo(step.Component.Container.Image.Name) {
				project, image, _, err := w.getter.RepoParts(step.Component.Container.Image.Name)
				if err != nil {
					return nil, err
				}

				harborInfo, err := w.getter.GetInfo(project, image, step.Component.Container.Image.Tag)
				if err != nil {
					return nil, err
				}

				if strings.TrimSpace(harborInfo.Entrypoint) != "" {
					sourceParts = append(sourceParts, harborInfo.Entrypoint)
				}
			}
		}

		// Add the arguments to the source. If may include the tool
		// executable, it may already have been added to the source as the
		// entrypoint.
		sourceParts = append(sourceParts, step.Arguments()...)

		// If the StdoutPath is not empty, then stdout of the command needs to go to
		// a named file.
		if step.StdoutPath != "" {
			sourceParts = append(sourceParts, fmt.Sprintf("> %s", step.StdoutPath))
		}

		// If the StderrPath is not empty, then stderr of the command needs to go to
		// a named file.
		if step.StderrPath != "" {
			sourceParts = append(sourceParts, fmt.Sprintf("2> %s", step.StderrPath))
		}

		// Assemble the source string for the script template.
		source = strings.Join(sourceParts, " ")

		stTmpl := v1alpha1.Template{
			Name: fmt.Sprintf("step-%d", idx),
			Script: &v1alpha1.ScriptTemplate{
				Source: source,
				Container: apiv1.Container{
					Resources: *resourcing.Requirements(w.analysis),
					Image: fmt.Sprintf(
						"%s:%s",
						step.Component.Container.Image.Name,
						step.Component.Container.Image.Tag,
					),

					Command:    []string{"bash"},
					WorkingDir: step.Component.Container.WorkingDirectory(),
					VolumeMounts: []apiv1.VolumeMount{
						{
							Name:      "workdir",
							MountPath: step.Component.Container.WorkingDirectory(),
						},
					},
				},
			},
		}

		templates = append(templates, stTmpl)
	}

	return templates, nil
}

// sendStatusStep generates a workflow step to send a status message to the DE.
func (w *WorkflowMaker) sendStatusStep(name, message, state string) *v1alpha1.WorkflowStep {
	return &v1alpha1.WorkflowStep{
		Name:     name,
		Template: "send-status",
		Arguments: v1alpha1.Arguments{
			Parameters: []v1alpha1.Parameter{
				{
					Name:  "message",
					Value: v1alpha1.AnyStringPtr(message),
				},
				{
					Name:  "state",
					Value: v1alpha1.AnyStringPtr(state),
				},
			},
		},
	}
}

// runStepsTemplates generates a list of templates that orchestrate the logic
// of a workflow.
func (w *WorkflowMaker) runStepsTemplates() ([]v1alpha1.Template, error) {
	// We generate a sequence of parallel steps consisting of single steps to
	// force the steps to run in sequence. Looks nicer in YAML than it does in
	// in code form.
	var templates []v1alpha1.Template
	var runSteps []v1alpha1.ParallelSteps

	stepTemplates, err := w.stepTemplates()
	if err != nil {
		return templates, err
	}

	runSteps = append(
		runSteps,
		v1alpha1.ParallelSteps{
			Steps: []v1alpha1.WorkflowStep{
				*w.sendStatusStep("downloading-files-status", "downloading files", "running"),
			},
		},
		v1alpha1.ParallelSteps{
			Steps: []v1alpha1.WorkflowStep{
				{
					Name:     "download-files",
					Template: "download-files",
				},
			},
		},
		v1alpha1.ParallelSteps{
			Steps: []v1alpha1.WorkflowStep{
				*w.sendStatusStep("done-downloading", "done downloading inputs", "running"),
			},
		},
	)

	for idx, st := range stepTemplates {
		runningName := fmt.Sprintf("running-%d", idx)
		runningMsg := fmt.Sprintf("running %d", idx)
		stepTmplName := fmt.Sprintf("step-%d", idx)
		doneRunningName := fmt.Sprintf("done-running-%d", idx)
		doneRunningMsg := fmt.Sprintf("done running %d", idx)

		runSteps = append(runSteps,
			v1alpha1.ParallelSteps{
				Steps: []v1alpha1.WorkflowStep{
					*w.sendStatusStep(runningName, runningMsg, statusRunning),
				},
			},
			v1alpha1.ParallelSteps{
				Steps: []v1alpha1.WorkflowStep{
					{
						Name:     st.Name,
						Template: stepTmplName,
					},
				},
			},
			v1alpha1.ParallelSteps{
				Steps: []v1alpha1.WorkflowStep{
					*w.sendStatusStep(doneRunningName, doneRunningMsg, statusRunning),
				},
			},
		)
	}

	templates = append(templates, v1alpha1.Template{
		Name:  "analysis-steps",
		Steps: runSteps,
	})

	templates = append(templates, stepTemplates...)

	return templates, nil
}

// exitHandlerTemplate returns the template definition for the
// steps taken when the workflow exits.
func (w *WorkflowMaker) exitHandlerTemplate() *v1alpha1.Template {
	return &v1alpha1.Template{
		Name: "analysis-exit-handler",
		Steps: []v1alpha1.ParallelSteps{
			{
				Steps: []v1alpha1.WorkflowStep{
					{
						Name:     "uploading-files-status",
						Template: "send-status",
						Arguments: v1alpha1.Arguments{
							Parameters: []v1alpha1.Parameter{
								{
									Name:  "message",
									Value: v1alpha1.AnyStringPtr("uploading files"),
								},
								{
									Name:  "state",
									Value: v1alpha1.AnyStringPtr(statusRunning),
								},
							},
						},
					},
				},
			},
			{
				Steps: []v1alpha1.WorkflowStep{
					{
						Name:     "upload-files",
						Template: "upload-files",
					},
				},
			},
			{
				Steps: []v1alpha1.WorkflowStep{
					{
						Name:     "finished-status",
						Template: "send-status",
						Arguments: v1alpha1.Arguments{
							Parameters: []v1alpha1.Parameter{
								{
									Name:  "message",
									Value: v1alpha1.AnyStringPtr("sending final status"),
								},
								{
									Name:  "state",
									Value: v1alpha1.AnyStringPtr("{{workflow.status}}"),
								},
							},
						},
					},
				},
			},
			{
				Steps: []v1alpha1.WorkflowStep{
					{
						Name:     "cleanup",
						Template: "send-cleanup",
					},
				},
			},
		},
	}
}

// sendStatusTemplate returns the template definition for the steps that send
// status updates to the DE backend.
func (w *WorkflowMaker) sendStatusTemplate(opts *BatchSubmissionOpts) *v1alpha1.Template {
	return &v1alpha1.Template{
		Name: "send-status",
		Inputs: v1alpha1.Inputs{
			Parameters: []v1alpha1.Parameter{
				{
					Name: "message",
				},
				{
					Name: "state",
				},
			},
		},
		Container: &apiv1.Container{
			Image: opts.StatusSenderImage,
			Command: []string{
				"curl",
			},
			Args: []string{
				"-v",
				"-H",
				"Content-Type: application/json",
				"-d",
				`{
				    "job_uuid" : "{{workflow.parameters.job_uuid}}",
     				"hostname" : "batch",
         			"message": "{{inputs.parameters.message}}",
            		"state" : "{{inputs.parameters.state}}"
     			}`,
				"http://webhook-eventsource-svc.argo-events/batch",
			},
		},
	}
}

// sendCleanupEvent returns the template definition for the steps that send
// status updates to the DE backend.
func (w *WorkflowMaker) sendCleanupEventTemplate(opts *BatchSubmissionOpts) *v1alpha1.Template {
	return &v1alpha1.Template{
		Name: "send-cleanup",
		Container: &apiv1.Container{
			Image: opts.StatusSenderImage,
			Command: []string{
				"curl",
			},
			Args: []string{
				"-v",
				"-H",
				"Content-Type: application/json",
				"-d",
				`{"uuid" : "{{workflow.parameters.job_uuid}}"}`,
				"http://webhook-eventsource-svc.argo-events/batch/cleanup",
			},
		},
	}
}

// downloadFilesTemplate returns a template definition for the steps that
// download files from the data store into the working directory volume.
func (w *WorkflowMaker) downloadFilesTemplate(opts *BatchSubmissionOpts) *v1alpha1.Template {
	var inputFilesAndFolders []string

	for _, stepInput := range w.analysis.Inputs() {
		inputFilesAndFolders = append(
			inputFilesAndFolders,
			stepInput.IRODSPath(),
		)
	}

	return &v1alpha1.Template{
		Name: "download-files",
		Container: &apiv1.Container{
			Image:      opts.FileTransferImage,
			WorkingDir: opts.FileTransferWorkingDir,
			VolumeMounts: []apiv1.VolumeMount{
				{
					Name:      defaultVolumeName,
					MountPath: opts.FileTransferWorkingDir,
				},
			},
			Args: append([]string{
				fmt.Sprintf("--log_level=%s", opts.FileTransferLogLevel),
				"get",
			},
				inputFilesAndFolders...,
			),
			Env: []apiv1.EnvVar{
				{
					Name:  "IRODS_CLIENT_USER_NAME",
					Value: "{{workflow.parameters.username}}",
				},
				{
					Name: "IRODS_HOST",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_HOST",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_PORT",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_PORT",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_USER_NAME",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_USER_NAME",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_USER_PASSWORD",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_USER_PASSWORD",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_ZONE_NAME",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_ZONE_NAME",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
			},
		},
	}
}

// uploadFilesTemplate returns a template used for the steps that uploads
// files to the data store.
func (w *WorkflowMaker) uploadFilesTemplate(opts *BatchSubmissionOpts) *v1alpha1.Template {
	return &v1alpha1.Template{
		Name: "upload-files",
		Container: &apiv1.Container{
			Image:      opts.FileTransferImage,
			WorkingDir: opts.FileTransferWorkingDir,
			VolumeMounts: []apiv1.VolumeMount{
				{
					Name:      defaultVolumeName,
					MountPath: opts.FileTransferWorkingDir,
				},
			},
			Args: []string{
				fmt.Sprintf("--log_level=%s", opts.FileTransferLogLevel),
				"put",
				"-f",
				"--no_root",
				".",
				"{{workflow.parameters.output-folder}}",
			},
			Env: []apiv1.EnvVar{
				{
					Name:  "IRODS_CLIENT_USER_NAME",
					Value: "{{workflow.parameters.username}}",
				},
				{
					Name: "IRODS_HOST",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_HOST",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_PORT",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_PORT",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_USER_NAME",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_USER_NAME",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_USER_PASSWORD",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_USER_PASSWORD",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
				{
					Name: "IRODS_ZONE_NAME",
					ValueFrom: &apiv1.EnvVarSource{
						ConfigMapKeyRef: &apiv1.ConfigMapKeySelector{
							Key: "IRODS_ZONE_NAME",
							LocalObjectReference: apiv1.LocalObjectReference{
								Name: "irods-config",
							},
						},
					},
				},
			},
		},
	}
}

// NewWorkflow returns a defintion of a workflow that executes a DE batch
// analyis. It does not submit the workflow to the cluster.
func (w *WorkflowMaker) NewWorkflow(opts *BatchSubmissionOpts) *v1alpha1.Workflow {
	var workflowTemplates []v1alpha1.Template
	stepsTemplates, err := w.runStepsTemplates()
	if err != nil {
		return nil
	}
	workflowTemplates = append(workflowTemplates, stepsTemplates...)
	workflowTemplates = append(
		workflowTemplates,
		*w.exitHandlerTemplate(),
		*w.sendStatusTemplate(opts),
		*w.sendCleanupEventTemplate(opts),
		*w.downloadFilesTemplate(opts),
		*w.uploadFilesTemplate(opts),
	)

	workflow := v1alpha1.Workflow{
		TypeMeta: v1.TypeMeta{
			Kind:       "Workflow",
			APIVersion: "argoproj.io/v1alpha1",
		},
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "batch-analysis-", // TODO: Make this configurable
			Namespace:    "argo",
			Labels: map[string]string{
				"job-uuid":    w.analysis.InvocationID,
				"external-id": w.analysis.InvocationID,
			},
		},
		Spec: v1alpha1.WorkflowSpec{
			ServiceAccountName: "argo-executor",         // TODO: Make this configurable
			Entrypoint:         "analysis-steps",        // TODO: Make this a const
			OnExit:             "analysis-exit-handler", // TODO: Make this a const
			Tolerations: []apiv1.Toleration{
				{
					Key:      "analysis",
					Operator: apiv1.TolerationOpExists,
				},
			},
			Affinity: &apiv1.Affinity{
				NodeAffinity: &apiv1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
						NodeSelectorTerms: []apiv1.NodeSelectorTerm{
							{
								MatchExpressions: []apiv1.NodeSelectorRequirement{
									{
										Key:      "analysis",
										Operator: apiv1.NodeSelectorOpExists,
									},
								},
							},
						},
					},
					PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.PreferredSchedulingTerm{
						{
							Weight: 1,
							Preference: apiv1.NodeSelectorTerm{
								MatchExpressions: []apiv1.NodeSelectorRequirement{
									{
										Key:      "batch",
										Operator: apiv1.NodeSelectorOpExists,
									},
								},
							},
						},
					},
				},
			},
			Arguments: v1alpha1.Arguments{
				Parameters: []v1alpha1.Parameter{
					{
						Name:  "username",
						Value: v1alpha1.AnyStringPtr(w.analysis.Submitter),
					},
					{
						Name:  "output-folder",
						Value: v1alpha1.AnyStringPtr(w.analysis.OutputDirectory()),
					},
					{
						Name:  "job_uuid",
						Value: v1alpha1.AnyStringPtr(w.analysis.InvocationID),
					},
				},
			},
			VolumeClaimTemplates: []apiv1.PersistentVolumeClaim{
				{
					ObjectMeta: v1.ObjectMeta{
						Name: defaultVolumeName,
					},
					Spec: apiv1.PersistentVolumeClaimSpec{
						AccessModes: []apiv1.PersistentVolumeAccessMode{
							apiv1.ReadWriteOnce,
						},
						Resources: apiv1.VolumeResourceRequirements{
							Requests: apiv1.ResourceList{
								apiv1.ResourceStorage: defaultStorageCapacity,
							},
						},
					},
				},
			},
			Templates: workflowTemplates,
		},
	}

	return &workflow
}

func (w *WorkflowMaker) SubmitWorkflow(ctx context.Context, serviceClient workflowpkg.WorkflowServiceClient, workflow *v1alpha1.Workflow) (*v1alpha1.Workflow, error) {
	creationOptions := &metav1.CreateOptions{}

	return serviceClient.CreateWorkflow(ctx, &workflowpkg.WorkflowCreateRequest{
		Namespace:     workflow.Namespace,
		Workflow:      workflow,
		ServerDryRun:  false,
		CreateOptions: creationOptions,
	})
}

func ListWorkflows(ctx context.Context, serviceClient workflowpkg.WorkflowServiceClient, namespace, labelKey, externalID string) (*v1alpha1.WorkflowList, error) {
	req, err := labels.NewRequirement(labelKey, selection.Equals, []string{externalID})
	if err != nil {
		return nil, err
	}
	return serviceClient.ListWorkflows(ctx, &workflowpkg.WorkflowListRequest{
		Namespace: namespace,
		ListOptions: &v1.ListOptions{
			LabelSelector: req.String(),
		},
	})
}

func StopWorkflows(ctx context.Context, serviceClient workflowpkg.WorkflowServiceClient, namespace, labelKey, externalID string) ([]v1alpha1.Workflow, error) {
	var retval []v1alpha1.Workflow
	workflows, err := ListWorkflows(ctx, serviceClient, namespace, labelKey, externalID)
	if err != nil {
		return nil, err
	}
	for _, workflow := range workflows.Items {
		if slices.Contains([]string{"Running", "Pending"}, string(workflow.Status.Phase)) {
			_, err := serviceClient.StopWorkflow(ctx, &workflowpkg.WorkflowStopRequest{
				Namespace: namespace,
				Name:      workflow.GetName(),
			})
			if err != nil {
				return nil, err
			}
		}

		if _, err = serviceClient.DeleteWorkflow(ctx, &workflowpkg.WorkflowDeleteRequest{
			Namespace: workflow.GetNamespace(),
			Name:      workflow.GetName(),
		}); err != nil {
			return nil, err
		}

		retval = append(retval, workflow)
	}
	return retval, nil
}

// SubmitWorkflow submits a workflow (probably created by GenerateWorkflow()) to the cluster.
// It does not wait for the workflow to complete. The context passed in needs to be the same
// one returned by NewWorkflowServiceClient.
func SubmitWorkflow(ctx context.Context, serviceClient workflowpkg.WorkflowServiceClient, workflow *v1alpha1.Workflow) (*v1alpha1.Workflow, error) {
	creationOptions := &metav1.CreateOptions{}

	return serviceClient.CreateWorkflow(ctx, &workflowpkg.WorkflowCreateRequest{
		Namespace:     workflow.Namespace,
		Workflow:      workflow,
		ServerDryRun:  false,
		CreateOptions: creationOptions,
	})
}

// StopWorkflow stops and deletes a workflow. The operation is asynchronous.
func StopWorkflow(ctx context.Context, serviceClient workflowpkg.WorkflowServiceClient, namespace, name string) (*v1alpha1.Workflow, error) {
	return serviceClient.StopWorkflow(ctx, &workflowpkg.WorkflowStopRequest{
		Namespace: namespace,
		Name:      name,
	})
}

// NewWorkflowServiceClient creates a WorkflowServiceClient that can be used to submit
// a workflow to the cluster with SubmitWorkflow().
func NewWorkflowServiceClient(c context.Context) (context.Context, workflowpkg.WorkflowServiceClient, error) {
	ctx, apiClient, err := client.NewAPIClient(c)
	if err != nil {
		return c, nil, err
	}
	serviceClient := apiClient.NewWorkflowServiceClient()
	return ctx, serviceClient, err
}
