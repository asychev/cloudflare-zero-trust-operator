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

	"github.com/pkg/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logger "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/bojanzelic/cloudflare-zero-trust-operator/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/bojanzelic/cloudflare-zero-trust-operator/internal/cfapi"
	"github.com/bojanzelic/cloudflare-zero-trust-operator/internal/config"
	cloudflare "github.com/cloudflare/cloudflare-go"
)

// CloudflareAccessGroupReconciler reconciles a CloudflareAccessGroup object
type CloudflareAccessGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cloudflare.zelic.io,resources=cloudflareaccessgroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cloudflare.zelic.io,resources=cloudflareaccessgroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cloudflare.zelic.io,resources=cloudflareaccessgroups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CloudflareAccessGroup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *CloudflareAccessGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	var existingCfAG *cloudflare.AccessGroup
	var api *cfapi.API

	log := logger.FromContext(ctx)
	ag := &v1alpha1.CloudflareAccessGroup{}

	err = r.Client.Get(ctx, req.NamespacedName, ag)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to get CloudflareAccessGroup", "CloudflareAccessGroup.Name", ag.Name)
		return ctrl.Result{}, errors.Wrap(err, "Failed to get CloudflareAccessGroup")
	}

	cfConfig := config.ParseCloudflareConfig(ag)
	validConfig, err := cfConfig.IsValid()
	if !validConfig {
		return ctrl.Result{}, errors.Wrap(err, "invalid config")
	}

	api, err = cfapi.New(cfConfig.APIToken, cfConfig.APIKey, cfConfig.APIEmail, cfConfig.AccountID)

	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "unable to initialize cloudflare object")
	}

	// Fetch user details on the account
	// @todo paginate
	cfAccessGroups, err := api.AccessGroups(ctx)
	if err != nil {
		return ctrl.Result{}, errors.Wrap(err, "unable to get access groups")
	}

	newCfAG := ag.ToCloudflare()

	if ag.Status.AccessGroupID == "" {
		for _, g := range cfAccessGroups {
			if g.Name == ag.CloudflareName() {
				//found todo
				log.Info(ag.CloudflareName() + " already exists")

				//update status to associate the group ID
				ag.Status.AccessGroupID = g.ID
				ag.Status.CreatedAt = v1.NewTime(*g.CreatedAt)
				ag.Status.UpdatedAt = v1.NewTime(*g.UpdatedAt)

				//re-intialize to update the status
				newCfAG = ag.ToCloudflare()

				existingCfAG = &g
				err := r.Status().Update(ctx, ag) //nolint
				if err != nil {
					return ctrl.Result{}, errors.Wrap(err, "unable to update access group")
				}

				break
			}
		}
	} else {
		cfAG, err := api.AccessGroup(ctx, ag.Status.AccessGroupID)
		existingCfAG = &cfAG
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to get access groups")
		}
	}

	if existingCfAG == nil {
		_, err = api.CreateAccessGroup(ctx, newCfAG)
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to create access group")
		}
	}

	if !cfapi.AcessGroupEmailEqual(*existingCfAG, newCfAG) {
		log.Info(newCfAG.Name + " has changed, updating...")

		_, err := api.UpdateAccessGroup(ctx, newCfAG)
		if err != nil {
			return ctrl.Result{}, errors.Wrap(err, "unable to update access groups")
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CloudflareAccessGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CloudflareAccessGroup{}).
		Complete(r)
}
