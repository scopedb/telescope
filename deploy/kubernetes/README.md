# Kubernetes deployment

The Kubernetes baseline uses a StatefulSet so each replica keeps a stable, independent persistent queue. Copy the example overlay, edit its mapping and image tag, then create the ScopeDB connection and apply it:

```bash
cp -R deploy/kubernetes/example deploy/kubernetes/local
# Edit deploy/kubernetes/local/telescope.yaml and pin newTag in kustomization.yaml.

export KUBE_CONTEXT=my-kubernetes-context
kubectl --context "$KUBE_CONTEXT" cluster-info
# The overlay declares this namespace too, but the secret below is created
# before the overlay is applied, so create the namespace first.
kubectl --context "$KUBE_CONTEXT" create namespace telescope --dry-run=client -o yaml | \
  kubectl --context "$KUBE_CONTEXT" apply -f -
kubectl --context "$KUBE_CONTEXT" -n telescope create secret generic telescope-scopedb \
  --from-literal=endpoint=https://<region>.scopedb.cloud \
  --from-literal=api-key=sk_... \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f -
kubectl --context "$KUBE_CONTEXT" apply -k deploy/kubernetes/local
```

Applications inside the cluster can export to `telescope.telescope:4317` or `http://telescope.telescope:4318`. Inspect the first replica and verify its end-to-end delivery directly:

```bash
kubectl --context "$KUBE_CONTEXT" -n telescope exec telescope-0 -- telescope status
kubectl --context "$KUBE_CONTEXT" -n telescope exec telescope-0 -- telescope verify
```

The baseline starts one replica with a 2 GiB queue volume. Each additional StatefulSet ordinal receives its own volume; size it using the [queue capacity guidance](../../docs/deployment.md#queue-ownership-and-sizing). Drain an ordinal before scaling it down.

Kustomize hashes the generated config and rolls the StatefulSet when it changes. Follow the [drain procedure](../../docs/operations.md#drain-and-upgrade) before changing a mapping in place, or route new traffic to a separate deployment while the old one drains. Edit runtime settings in the overlay's `telescope.yaml`; credentials belong in the Secret. Commands work from the source or extracted release directory.
