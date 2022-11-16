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
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	installerv1alpha1 "github.com/mrsimonemms/kubebuilder/api/v1alpha1"
	"github.com/mrsimonemms/kubebuilder/pkg/resources"
	"github.com/mrsimonemms/kubebuilder/pkg/rest"
)

// ConfigReconciler reconciles a Config object
type ConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

	var operatorConfig = &installerv1alpha1.Config{}
	if err := r.Get(ctx, req.NamespacedName, operatorConfig); err != nil {
		log.Error(err, "unable to fetch client")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	operatorConfigOld := operatorConfig.DeepCopy()

	if operatorConfig.Status.InstallerStatus == "" {
		operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypePending
	}

	log.Info("SJE: " + operatorConfig.Status.InstallerStatus.String())

	switch operatorConfig.Status.InstallerStatus {
	case installerv1alpha1.InstallerStatusTypePending:
		operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeRunning

		err := r.Status().Update(context.TODO(), operatorConfig)
		if err != nil {
			log.Error(err, "failed to update client status")
			return ctrl.Result{}, err
		} else {
			log.Info("updated client status: " + operatorConfig.Status.InstallerStatus.String())
			return ctrl.Result{Requeue: true}, nil
		}
	case installerv1alpha1.InstallerStatusTypeRunning:
		pod := resources.CreatePod(operatorConfig)

		query := &corev1.Pod{}
		err := r.Client.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: pod.ObjectMeta.Name}, query)
		if err != nil && errors.IsNotFound(err) {
			if operatorConfig.Status.LastPodName == "" {
				err = ctrl.SetControllerReference(operatorConfig, pod, r.Scheme)
				if err != nil {
					return ctrl.Result{}, err
				}

				err = r.Create(context.TODO(), pod)
				if err != nil {
					return ctrl.Result{}, err
				}

				log.Info("pod created successfully", "name", pod.Name)

				return ctrl.Result{}, nil
			} else {
				operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeCleaning
			}
		} else if err != nil {
			log.Error(err, "cannot get pod")
			return ctrl.Result{}, err
		} else if query.Status.Phase == corev1.PodFailed ||
			query.Status.Phase == corev1.PodSucceeded {
			log.Info("container terminated", "reason", query.Status.Reason, "message", query.Status.Message)

			operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeCleaning
		} else if query.Status.Phase == corev1.PodRunning {
			if operatorConfig.Status.LastPodName != operatorConfig.Spec.InstallerImage {
				if query.Status.ContainerStatuses[0].Ready {
					log.Info("Trying to bind to: " + query.Status.PodIP)

					if !rest.GetClient(operatorConfig, query.Status.PodIP) {
						if rest.BindClient(operatorConfig, query.Status.PodIP) {
							log.Info("Client" /*+ installerConfig.Spec.ClientId*/ + " is binded to pod " + query.ObjectMeta.GetName() + ".")
							operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeCleaning
						} else {
							log.Info("Client not added.")
						}
					} else {
						log.Info("Client binded already.")
					}
				} else {
					log.Info("Container not ready, reschedule bind")
					return ctrl.Result{Requeue: true}, err
				}

				log.Info("Client last pod name: " + operatorConfig.Status.LastPodName)
				log.Info("Pod is running.")
			}
		} else if query.Status.Phase == corev1.PodPending {
			return ctrl.Result{Requeue: true}, nil
		} else {
			return ctrl.Result{Requeue: true}, err
		}

		if !reflect.DeepEqual(operatorConfigOld.Status, operatorConfig.Status) {
			err = r.Status().Update(context.TODO(), operatorConfig)
			if err != nil {
				log.Error(err, "failed to update client status from running")
				return ctrl.Result{}, err
			} else {
				log.Info("updated client status RUNNING -> " + operatorConfig.Status.InstallerStatus.String())
				return ctrl.Result{Requeue: true}, nil
			}
		}
	case installerv1alpha1.InstallerStatusTypeCleaning:
		query := &corev1.Pod{}
		HasClients := rest.HasClients(operatorConfig, query.Status.PodIP)

		err := r.Client.Get(ctx, client.ObjectKey{Namespace: operatorConfig.Namespace, Name: operatorConfig.Status.LastPodName}, query)
		if err == nil && operatorConfig.ObjectMeta.DeletionTimestamp.IsZero() {
			if !HasClients {
				err = r.Delete(context.TODO(), query)
				if err != nil {
					log.Error(err, "Failed to remove old pod")
					return ctrl.Result{}, err
				} else {
					log.Info("Old pod removed")
					return ctrl.Result{Requeue: true}, nil
				}
			}
		}

		if operatorConfig.Status.LastPodName != operatorConfig.Spec.InstallerImage {
			operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypeRunning
			operatorConfig.Status.LastPodName = operatorConfig.Spec.InstallerImage
		} else {
			operatorConfig.Status.InstallerStatus = installerv1alpha1.InstallerStatusTypePending
			operatorConfig.Status.LastPodName = ""
		}

		if !reflect.DeepEqual(operatorConfigOld.Status, operatorConfig.Status) {
			err = r.Status().Update(context.TODO(), operatorConfig)
			if err != nil {
				log.Error(err, "failed to update client status from cleaning")
				return ctrl.Result{}, err
			} else {
				log.Info("updated client status CLEANING -> " + operatorConfig.Status.InstallerStatus.String())
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&installerv1alpha1.Config{}).
		Complete(r)
}
