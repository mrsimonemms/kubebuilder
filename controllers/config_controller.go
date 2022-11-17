/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ref "k8s.io/client-go/tools/reference"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	installerv1alpha1 "github.com/mrsimonemms/kubebuilder/api/v1alpha1"
	"github.com/mrsimonemms/kubebuilder/pkg/resources"
)

var (
	jobOwnerKey = ".metadata.name"
	apiGVStr    = installerv1alpha1.GroupVersion.String()
)

// ConfigReconciler reconciles a Config object
type ConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func isJobFinished(job *batchv1.Job) (bool, batchv1.JobConditionType) {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true, c.Type
		}
	}

	return false, ""
}

//+kubebuilder:rbac:groups=installer.gitpod.io,resources=configs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=installer.gitpod.io,resources=configs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=installer.gitpod.io,resources=configs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Config object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *ConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var activeJobs []*batchv1.Job
	var successfulJobs []*batchv1.Job
	var failedJobs []*batchv1.Job
	var mostRecentTime *time.Time // find the last run so we can update the status

	var operatorConfig = &installerv1alpha1.Config{}
	if err := r.Get(ctx, req.NamespacedName, operatorConfig); err != nil {
		log.Error(err, "unable to fetch config")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var childJobs batchv1.JobList
	if err := r.List(ctx, &childJobs, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name}); err != nil {
		log.Error(err, "unable to list child Jobs")
		return ctrl.Result{}, err
	}

	for i, job := range childJobs.Items {
		_, finishedType := isJobFinished(&job)
		switch finishedType {
		case "": // ongoing job
			activeJobs = append(activeJobs, &childJobs.Items[i])
		case batchv1.JobFailed:
			failedJobs = append(failedJobs, &childJobs.Items[i])
		case batchv1.JobComplete:
			successfulJobs = append(successfulJobs, &childJobs.Items[i])
		}
	}

	if mostRecentTime != nil {
		operatorConfig.Status.LastScheduleTime = &metav1.Time{Time: *mostRecentTime}
	} else {
		operatorConfig.Status.LastScheduleTime = nil
	}
	operatorConfig.Status.Active = nil
	for _, activeJob := range activeJobs {
		jobRef, err := ref.GetReference(r.Scheme, activeJob)
		if err != nil {
			log.Error(err, "unable to make reference to active job", "job", activeJob)
			continue
		}
		operatorConfig.Status.Active = append(operatorConfig.Status.Active, *jobRef)
	}

	log.V(1).Info("job count", "active jobs", len(activeJobs), "successful jobs", len(successfulJobs), "failed jobs", len(failedJobs))

	for _, operatorResource := range resources.CreateResources(operatorConfig) {
		if err := r.Create(ctx, operatorResource); err != nil {
			log.Error(err, "unable to create resource for Installer", operatorResource.GetObjectKind().GroupVersionKind().Kind, operatorResource)
			return ctrl.Result{}, err
		}
		log.V(1).Info("created resource for Installer run", operatorResource.GetObjectKind().GroupVersionKind().Kind, operatorResource)
	}

	// operatorConfigOld := operatorConfig.DeepCopy()

	// if operatorConfig.Status.InstallerStatus == "" {
	// 	operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypePending
	// }

	// switch operatorConfig.Status.InstallerStatus {
	// case installerv1alpha1.InstallerStatusTypePending:
	// 	operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeRunning

	// 	err := r.Status().Update(context.TODO(), operatorConfig)
	// 	if err != nil {
	// 		log.Error(err, "failed to update client status")
	// 		return ctrl.Result{}, err
	// 	} else {
	// 		log.Info("updated client status: " + operatorConfig.Status.InstallerStatus.String())
	// 		return ctrl.Result{Requeue: true}, nil
	// 	}
	// case installerv1alpha1.InstallerStatusTypeRunning:
	// 	pod := resources.CreatePod(operatorConfig)

	// 	query := &corev1.Pod{}
	// 	err := r.Client.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: pod.ObjectMeta.Name}, query)
	// 	if err != nil && errors.IsNotFound(err) {
	// 		if operatorConfig.Status.LastPodName == "" {
	// 			err = ctrl.SetControllerReference(operatorConfig, pod, r.Scheme)
	// 			if err != nil {
	// 				return ctrl.Result{}, err
	// 			}

	// 			err = r.Create(context.TODO(), pod)
	// 			if err != nil {
	// 				return ctrl.Result{}, err
	// 			}

	// 			log.Info("pod created successfully", "name", pod.Name)

	// 			return ctrl.Result{}, nil
	// 		} else {
	// 			operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeCleaning
	// 		}
	// 	} else if err != nil {
	// 		log.Error(err, "cannot get pod")
	// 		return ctrl.Result{}, err
	// 	} else if query.Status.Phase == corev1.PodFailed ||
	// 		query.Status.Phase == corev1.PodSucceeded {
	// 		log.Info("container terminated", "reason", query.Status.Reason, "message", query.Status.Message)

	// 		operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeCleaning
	// 	} else if query.Status.Phase == corev1.PodRunning {
	// 		if operatorConfig.Status.LastPodName != operatorConfig.Spec.InstallerImage {
	// 			if query.Status.ContainerStatuses[0].Ready {
	// 				log.Info("Trying to bind to: " + query.Status.PodIP)

	// 				if !rest.GetClient(operatorConfig, query.Status.PodIP) {
	// 					if rest.BindClient(operatorConfig, query.Status.PodIP) {
	// 						log.Info("Client" /*+ installerConfig.Spec.ClientId*/ + " is binded to pod " + query.ObjectMeta.GetName() + ".")
	// 						operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeCleaning
	// 					} else {
	// 						log.Info("Client not added.")
	// 					}
	// 				} else {
	// 					log.Info("Client binded already.")
	// 				}
	// 			} else {
	// 				log.Info("Container not ready, reschedule bind")
	// 				return ctrl.Result{Requeue: true}, err
	// 			}

	// 			log.Info("Client last pod name: " + operatorConfig.Status.LastPodName)
	// 			log.Info("Pod is running.")
	// 		}
	// 	} else if query.Status.Phase == corev1.PodPending {
	// 		return ctrl.Result{Requeue: true}, nil
	// 	} else {
	// 		return ctrl.Result{Requeue: true}, err
	// 	}

	// 	if !reflect.DeepEqual(operatorConfigOld.Status, operatorConfig.Status) {
	// 		err = r.Status().Update(context.TODO(), operatorConfig)
	// 		if err != nil {
	// 			log.Error(err, "failed to update client status from running")
	// 			return ctrl.Result{}, err
	// 		} else {
	// 			log.Info("updated client status RUNNING -> " + operatorConfig.Status.InstallerStatus.String())
	// 			return ctrl.Result{Requeue: true}, nil
	// 		}
	// 	}
	// case installerv1alpha1.InstallerStatusTypeCleaning:
	// 	query := &corev1.Pod{}
	// 	HasClients := rest.HasClients(operatorConfig, query.Status.PodIP)

	// 	err := r.Client.Get(ctx, client.ObjectKey{Namespace: operatorConfig.Namespace, Name: operatorConfig.Status.LastPodName}, query)
	// 	if err == nil && operatorConfig.ObjectMeta.DeletionTimestamp.IsZero() {
	// 		if !HasClients {
	// 			err = r.Delete(context.TODO(), query)
	// 			if err != nil {
	// 				log.Error(err, "Failed to remove old pod")
	// 				return ctrl.Result{}, err
	// 			} else {
	// 				log.Info("Old pod removed")
	// 				return ctrl.Result{Requeue: true}, nil
	// 			}
	// 		}
	// 	}

	// 	if operatorConfig.Status.LastPodName != operatorConfig.Spec.InstallerImage {
	// 		operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeRunning
	// 		operatorConfig.Status.LastPodName = operatorConfig.Spec.InstallerImage
	// 	} else {
	// 		operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypePending
	// 		operatorConfig.Status.LastPodName = ""
	// 	}

	// 	if !reflect.DeepEqual(operatorConfigOld.Status, operatorConfig.Status) {
	// 		err = r.Status().Update(context.TODO(), operatorConfig)
	// 		if err != nil {
	// 			log.Error(err, "failed to update client status from cleaning")
	// 			return ctrl.Result{}, err
	// 		} else {
	// 			log.Info("updated client status CLEANING -> " + operatorConfig.Status.InstallerStatus.String())
	// 			return ctrl.Result{Requeue: true}, nil
	// 		}
	// 	}
	// }

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &batchv1.Job{}, jobOwnerKey, func(rawObj client.Object) []string {
		// grab the job object, extract the owner...
		job := rawObj.(*batchv1.Job)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ...make sure it's a CronJob...
		if owner.APIVersion != apiGVStr || owner.Kind != "CronJob" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&installerv1alpha1.Config{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
