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

The baseline starts one replica with a 2 GiB queue volume. `TELESCOPE_QUEUE_MAX_BYTES` is the per-signal logical serialized-telemetry budget (512 MiB by default), while the volume must additionally hold the queue database pages reported by `queue_storage.allocated_bytes`; size the volume above the combined capacities of all enabled signals. Every additional StatefulSet ordinal receives its own volume; drain an ordinal before scaling it down. Kustomize gives the generated config a content hash and rolls the StatefulSet when the mapping changes. Apply such a change in place only after the existing queues and accepted-without-final-outcome counts reach zero. Otherwise deploy a second instance with distinct names, selectors, and volumes, route new OTLP to it, and let the old instance drain under its original config.

Run these commands from the source or extracted release directory. Change Collector tuning through a StatefulSet environment patch in your overlay; credentials belong in the Secret.
