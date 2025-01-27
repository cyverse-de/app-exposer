package batch

import (
	"context"
	"fmt"

	"github.com/argoproj/argo-workflows/v3/cmd/argo/commands/client"
	workflowpkg "github.com/argoproj/argo-workflows/v3/pkg/apiclient/workflow"
	v1alpha1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/cyverse-de/model/v6"
	apiv1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	defaultStorageCapacity, _ = resourcev1.ParseQuantity("5Gi")
	defaultStorageMode        = "ReadWriteOnce"
	defaultVolumeName         = "workdir"
	statusRunning             = "running"
)

type BatchSubmissionOpts struct {
	FileTransferImage      string
	FileTransferLogLevel   string
	FileTransferWorkingDir string
	StatusSenderImage      string
	AnalysisID             string
}

// stepTemplates creates a list of templates based on the steps
// defined in the job description.
func stepTemplates(job *model.Job) []v1alpha1.Template {
	var templates []v1alpha1.Template

	for idx, step := range job.Steps {
		allArgs := step.Arguments()
		var cmd []string
		var args []string

		if len(allArgs) > 0 {
			cmd = []string{allArgs[0]}

			if len(allArgs) > 1 {
				args = allArgs[1:]
			}
		}

		templates = append(
			templates,
			v1alpha1.Template{
				Name: fmt.Sprintf("step-%d", idx),
				Container: &apiv1.Container{
					Image: fmt.Sprintf(
						"%s:%s",
						step.Component.Container.Image.Name,
						step.Component.Container.Image.Tag,
					),
					Command:    cmd,
					Args:       args,
					WorkingDir: step.Component.Container.WorkingDirectory(),
					VolumeMounts: []apiv1.VolumeMount{
						{
							Name:      "workdir",
							MountPath: step.Component.Container.WorkingDirectory(),
						},
					},
				},
			},
		)
	}

	return templates
}

// sendStatusStep generates a workflow step to send a status message to the DE.
func sendStatusStep(name, message, state string) *v1alpha1.WorkflowStep {
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
func runStepsTemplates(job *model.Job) []v1alpha1.Template {
	// We generate a sequence of parallel steps consisting of single steps to
	// force the steps to run in sequence. Looks nicer in YAML than it does in
	// in code form.
	var templates []v1alpha1.Template
	var runSteps []v1alpha1.ParallelSteps

	stepTemplates := stepTemplates(job)

	runSteps = append(
		runSteps,
		v1alpha1.ParallelSteps{
			Steps: []v1alpha1.WorkflowStep{
				*sendStatusStep("downloading-files-status", "downloading files", "running"),
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
				*sendStatusStep("done-downloading", "done downloading inputs", "running"),
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
					*sendStatusStep(runningName, runningMsg, statusRunning),
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
					*sendStatusStep(doneRunningName, doneRunningMsg, statusRunning),
				},
			},
		)
	}

	templates = append(templates, v1alpha1.Template{
		Name:  "analysis-steps",
		Steps: runSteps,
	})

	templates = append(templates, stepTemplates...)

	return templates
}

// exitHandlerTemplate returns the template definition for the
// steps taken when the workflow exits.
func exitHandlerTemplate() *v1alpha1.Template {
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
		},
	}
}

// sendStatusTemplate returns the template definition for the steps that send
// status updates to the DE backend.
func sendStatusTemplate(opts *BatchSubmissionOpts) *v1alpha1.Template {
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
                       			"analysis_uuid" : "{{workflow.parameters.analysis_uuid}}",
                          		"hostname" : "test",
                            	"message": "{{inputs.parameters.message}}",
                             	"state" : "{{inputs.parameters.state}}"
            				}`,
				"http://webhook-eventsource-svc.argo-events/batch",
			},
		},
	}
}

// downloadFilesTemplate returns a template definition for the steps that
// download files from the data store into the working directory volume.
func downloadFilesTemplate(job *model.Job, opts *BatchSubmissionOpts) *v1alpha1.Template {
	var inputFilesAndFolders []string

	for _, stepInput := range job.Inputs() {
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
func uploadFilesTemplate(opts *BatchSubmissionOpts) *v1alpha1.Template {
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
func NewWorkflow(job *model.Job, opts *BatchSubmissionOpts) *v1alpha1.Workflow {
	var workflowTemplates []v1alpha1.Template
	workflowTemplates = append(workflowTemplates, runStepsTemplates(job)...)
	workflowTemplates = append(workflowTemplates, stepTemplates(job)...)
	workflowTemplates = append(
		workflowTemplates,
		*exitHandlerTemplate(),
		*sendStatusTemplate(opts),
		*downloadFilesTemplate(job, opts),
		*uploadFilesTemplate(opts),
	)

	workflow := v1alpha1.Workflow{
		TypeMeta: v1.TypeMeta{
			Kind:       "Workflow",
			APIVersion: "argoproj.io/v1alpha1",
		},
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "batch-analysis-", // TODO: Make this configurable
			Namespace:    "argo",
		},
		Spec: v1alpha1.WorkflowSpec{
			ServiceAccountName: "argo-executor",         // TODO: Make this configurable
			Entrypoint:         "analysis-steps",        // TODO: Make this a const
			OnExit:             "analysis-exit-handler", // TODO: Make this a const
			Arguments: v1alpha1.Arguments{
				Parameters: []v1alpha1.Parameter{
					{
						Name:  "username",
						Value: v1alpha1.AnyStringPtr(job.Submitter),
					},
					{
						Name:  "output-folder",
						Value: v1alpha1.AnyStringPtr(job.OutputDirectory()),
					},
					{
						Name:  "job_uuid",
						Value: v1alpha1.AnyStringPtr(job.InvocationID),
					},
					{
						Name:  "analysis_uuid",
						Value: v1alpha1.AnyStringPtr(opts.AnalysisID),
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

// SubmitWorkflow submits a workflow (probably created by GenerateWorkflow()) to the cluster.
// It does not wait for the workflow to complete.
func SubmitWorkflow(ctx context.Context, serviceClient workflowpkg.WorkflowServiceClient, workflow *v1alpha1.Workflow) (*v1alpha1.Workflow, error) {
	creationOptions := &metav1.CreateOptions{}

	return serviceClient.CreateWorkflow(ctx, &workflowpkg.WorkflowCreateRequest{
		Namespace:     workflow.Namespace,
		Workflow:      workflow,
		ServerDryRun:  false,
		CreateOptions: creationOptions,
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
