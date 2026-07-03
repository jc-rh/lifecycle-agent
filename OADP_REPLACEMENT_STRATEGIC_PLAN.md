# Strategic Plan: Replace OADP with Built-in LCA Backup/Restore

## Executive Summary

This plan outlines the strategy to eliminate the OADP (OpenShift API for Data Protection) operator dependency from the Lifecycle Agent by implementing a built-in backup/restore solution using local node storage. 

**Two Recommended Approaches**:

1. **Option 4B (Always-On MinIO)** ⭐ **FASTEST** — 2-3 days implementation, 0s runtime overhead, full OADP features, +1GB RAM (trivial on 256GB telco SNO hardware)

2. **Option 1 (Custom Backup, AI-Assisted)** — 3-4 weeks implementation (with AI coding tools like Claude Code/Copilot), 0s runtime overhead, zero dependencies, simpler long-term architecture

Traditional custom implementation without AI would take 7-9 weeks, but with modern AI-assisted development tools, this reduces to **3-4 weeks**.

## 1. Current State Analysis

### 1.1 OADP Integration Overview

**PrePivot Backup Flow:**
- `controllers/upgrade_handlers.go:146`: `HandleBackup()` orchestrates OADP backup creation
- `internal/backuprestore/backup.go:337-428`: Exports DataProtectionApplication (DPA) and S3 credentials as ConfigMaps
- `internal/backuprestore/backup.go:144-213`: Creates OADP Backup CR with resource filters and hooks
- OADP operator backs up Kubernetes resources to external S3 storage
- PV metadata backed up; actual PV contents remain on disk (`/var/lib/containers` partition persists across stateroots)

**PostPivot Restore Flow:**
- `controllers/upgrade_handlers.go:438`: `HandleRestore()` orchestrates restoration
- `internal/backuprestore/restore.go:95-150`: Imports DPA/credentials from ConfigMaps, creates OADP Restore CR
- `internal/backuprestore/restore.go:216-390`: Wave-based restoration with resource ordering (namespaces first, then PVs, workloads, etc.)
- OADP operator pulls resources from S3 and applies to new stateroot

**External Dependencies:**
- OADP operator must be pre-installed and configured
- S3-compatible storage endpoint (external or MinIO)
- S3 credentials management (secrets/ConfigMaps)
- Network connectivity to S3 during backup/restore operations

### 1.2 Key Constraints

**Storage Architecture:**
- **Stateroot isolation**: `/ostree/deploy/rhcos/deploy/<stateroot-hash>` contains OS and core services
- **Shared partition**: `/var/lib/containers` is a separate XFS partition mounted across all stateroots (per `internal/ostreeclient/client.go:89-107`)
- **PV persistence**: Volume data in `/var/lib/containers` survives pivot; only metadata (PV/PVC definitions) needs backup
- **Disk space**: `/var/lib/containers` typically has 50-200GB available on SNO nodes

**Operational Constraints:**
- **Single Node OpenShift (SNO)**: No distributed storage, all data local to node
- **Upgrade atomicity**: Pivot to new stateroot must be transactional; restore failure must allow rollback
- **Backward compatibility**: Existing clusters using OADP must upgrade smoothly
- **Air-gapped environments**: Solution must work without internet access (no cloud storage)

**Technical Constraints:**
- **Resource serialization**: Code already exports resources to YAML (`internal/clusterconfig/export.go` exports cluster configs)
- **Wave-based restore**: Current restore logic in `restore.go:216-390` applies resources in dependency order (must preserve)
- **Velero hooks**: Pre/post-backup hooks for application consistency (may need re-implementation)

### 1.3 Why Replace OADP?

**Benefits of Removal:**

| Benefit | What We Gain | Quantification |
|---------|--------------|----------------|
| **Eliminate external dependency** | No OADP operator installation/maintenance, simpler deployment | -1 operator, -500MB memory overhead, -3 CRDs |
| **Remove S3 requirement** | No S3 storage costs, no network dependency, works fully air-gapped | $0 storage costs, zero network traffic during upgrade |
| **Simplify architecture** | Fewer moving parts, easier troubleshooting, direct control over backup format | -200+ lines of OADP integration code (`backup.go:337-428`, DPA export logic) |
| **Faster restore** | Local disk I/O (500+ MB/s) vs S3 network (10-100 MB/s) | 5-10x faster restore for large resource sets |
| **Built-in solution** | Users don't need to configure/understand OADP, better UX | Reduces installation steps from ~10 to ~3 |

**Costs of Removal:**

| Cost | What We Lose | Mitigation Strategy |
|------|--------------|---------------------|
| **Velero ecosystem** | Pre-built backup hooks, CSI snapshot integration, tested backup logic | Re-implement critical hooks in LCA; CSI snapshots not used (PVs persist across pivot) |
| **Offsite backup capability** | S3 storage allows disaster recovery if node fails | Document manual backup export procedure for critical clusters; local backup sufficient for upgrade/rollback |
| **Community support** | OADP is maintained by Red Hat, battle-tested | LCA team owns backup logic; gain control but assume maintenance burden |
| **Development effort** | Must build and maintain backup/restore code | Estimated 7-9 weeks (Phase 1-4 below); simpler than OADP integration long-term |

**Net Assessment**: Benefits outweigh costs for the IBU use case. OADP is designed for disaster recovery across clusters; LCA only needs local state preservation for upgrade/rollback. Local storage is faster, simpler, and removes external dependencies critical for edge/air-gapped SNO deployments.

## 2. Architecture Options

### Option 1: Kubernetes-Native YAML Export (RECOMMENDED)

**Description**: Export Kubernetes resources as YAML manifest files to `/var/lib/containers/lca-backups/<timestamp>/`, restore by applying manifests using `kubectl apply`.

**Pros:**
- **Leverages existing code**: `internal/clusterconfig/export.go` already serializes resources to YAML; reuse this pattern
- **Human-readable backups**: YAML files can be inspected/edited for troubleshooting (vs opaque tar archives)
- **Simple restore logic**: Standard `kubectl apply` with existing wave-based ordering (`restore.go:216-390` logic preserved)
- **Low disk overhead**: YAML compression with gzip achieves 5-10:1 ratio; typical backup <1GB for SNO workloads
- **No external tools**: Pure Go using `client-go` APIs, no dependency on `tar` or `velero` binaries

**Cons:**
- **File count**: Thousands of small YAML files vs single archive (slower on some filesystems)
- **Manual integrity checking**: Need to implement checksum validation (OADP has built-in)
- **No CSI snapshot integration**: Must document that PV data persistence relies on `/var/lib/containers` partition (acceptable: PVs already persist)

**Trade-offs:**
- **Gain**: Simplicity, transparency, reuse of existing export patterns, fast local I/O
- **Give up**: OADP's ecosystem features (hooks, CSI snapshots) — but these aren't needed for LCA's upgrade use case

**Implementation Complexity**: **Low**
- Reuse `export.go` resource serialization
- Directory structure: `/var/lib/containers/lca-backups/<ibu-name>-<timestamp>/{namespaces,pvs,pvcs,deployments,...}.yaml`
- Restore: Sequential YAML apply with retry logic

**Risk Assessment**:
- **Risk**: YAML format changes between Kubernetes versions → **Mitigation**: Store API version in metadata, test upgrade paths (1.28→1.29, etc.)
- **Risk**: Disk space exhaustion → **Mitigation**: Pre-flight check for 10GB free space, auto-cleanup of old backups (keep last 3)
- **Risk**: Partial restore failure → **Mitigation**: Transactional apply (track applied resources, rollback on failure)

---

### Option 2: Tar Archive with Resource Bundles

**Description**: Serialize resources to YAML, bundle into a single `.tar.gz` archive at `/var/lib/containers/lca-backups/<timestamp>.tar.gz`.

**Pros:**
- **Single file**: Easier to copy/transfer for manual backup export
- **Atomic writes**: Tar creation is atomic (write to temp, rename on success)
- **Built-in compression**: Gzip compression integrated
- **Fewer inodes**: One file vs thousands (better for XFS with limited inodes)

**Cons:**
- **Requires tar binary**: Must ensure `tar` is available in LCA container image (adds dependency)
- **Opaque format**: Cannot inspect individual resources without extracting entire archive
- **Slower random access**: Must extract full archive before restore (vs reading individual YAMLs on demand)
- **More complex restore**: Extract archive, then apply YAMLs (two-step vs single apply loop)

**Trade-offs:**
- **Gain**: Single-file simplicity, atomic writes, fewer inodes
- **Give up**: Transparency, fast random access, dependency-free implementation

**Implementation Complexity**: **Medium**
- Requires tar library (`archive/tar` in Go stdlib) or shell-out to `tar` binary
- Extract logic adds complexity to restore flow
- Error handling for corrupt archives

**Risk Assessment**:
- **Risk**: Tar corruption during write → **Mitigation**: Write to temp file, verify archive integrity, rename only on success
- **Risk**: Extraction failure mid-restore → **Mitigation**: Extract to temp directory, validate all files present before applying

---

### Option 3: Hybrid Approach (Local Primary, Optional S3 Secondary)

**Description**: Implement local YAML backup as primary; optionally support OADP S3 backup as secondary for users who want offsite backup.

**Pros:**
- **Flexibility**: Users can choose local-only (default) or local+S3 (opt-in)
- **Backward compatibility**: Existing OADP users can continue using S3
- **Best of both worlds**: Fast local restore, offsite disaster recovery option

**Cons:**
- **Maintains OADP dependency**: Still requires OADP operator for S3 mode (doesn't achieve goal of removing dependency)
- **Dual code paths**: Must maintain both local and OADP backup logic (2x complexity)
- **User confusion**: Two backup modes increases cognitive load, documentation burden
- **Delayed benefit**: Doesn't eliminate S3 setup for existing users (they must opt into local mode)

**Trade-offs:**
- **Gain**: Backward compatibility, optional disaster recovery
- **Give up**: Simplicity, full dependency removal, clear migration path

**Implementation Complexity**: **High**
- Must maintain both local and OADP code paths
- Feature flag logic to switch modes
- Migration tooling for OADP→local transition

**Risk Assessment**:
- **Risk**: Code divergence between local/OADP paths → **Mitigation**: Shared interface, integration tests for both paths
- **Risk**: Users stuck on OADP due to fear of change → **Mitigation**: Strong default to local, clear deprecation timeline

---

### Option 4: Keep OADP, Use Local MinIO Storage

**Description**: Configure OADP to use MinIO (S3-compatible) deployed on local storage instead of external S3. Two deployment patterns: always-on (static) or on-demand (ephemeral).

**Sub-Option 4A: On-Demand MinIO (Deploy During IBU)**
- **PrePivot**: Deploy MinIO StatefulSet → Wait for ready (~1-2 min) → Configure OADP → Run backup → MinIO persists through pivot
- **PostPivot**: Restore MinIO pod → Run restore → Delete MinIO after success
- **Resource overhead**: 0 MB idle, 512 MB-1 GB during IBU
- **Time overhead**: +1-2 minutes for deployment/config

**Sub-Option 4B: Always-On MinIO (Static Deployment)** ⭐ **RECOMMENDED for Telco SNO**
- **Pre-deployment**: Deploy MinIO StatefulSet once (day-0 or alongside OADP operator)
- **PrePivot**: OADP already configured, run backup immediately (existing code, 0s overhead)
- **PostPivot**: MinIO pod/PVC persist through pivot, run restore immediately (0s overhead)
- **Resource overhead**: 1 GB continuous (0.4% of typical 256GB telco SNO hardware)
- **Time overhead**: **0 seconds** — preserves IBU speed goals

**Architecture (Both Sub-Options)**:
- **Storage**: MinIO data stored in `/var/lib/containers/minio-storage/` on local PVC (same partition as Option 1)
- **OADP Integration**: Existing `backup.go` and `restore.go` code works unchanged, just point DPA to MinIO endpoint

**Pros:**
- **Full OADP feature set preserved**: Backup hooks (pre/post scripts), CSI snapshots, wave-based ordering, resource filtering — all work without custom code
- **Fast implementation**: 2-3 days (always-on) or 3-5 days (on-demand) vs 9 weeks for Option 1
- **Proven orchestration**: Velero's battle-tested logic handles edge cases, retries, webhooks
- **Migration path**: Can switch to external S3 later by changing DataProtectionApplication config
- **Lower risk**: Reuses existing OADP integration (`backup.go`, `restore.go`) with minimal changes
- **Sub-Option 4B (Always-On) — Zero time overhead**: Preserves IBU speed goals, 0 seconds added to backup/restore operations
- **Sub-Option 4B (Always-On) — Trivial resource cost on telco hardware**: 1 GB = **0.4% of 256GB** typical SNO RAM

**Cons:**
- **Sub-Option 4A (On-Demand) — Time overhead**: +1-2 minutes deployment/config negates speed benefits for fast IBUs (<15 min total)
- **Sub-Option 4A (On-Demand) — Complexity**: Must manage MinIO lifecycle (deploy, wait, cleanup)
- **Sub-Option 4B (Always-On) — Continuous memory**: 1 GB persistent (acceptable on 256GB+ telco SNO, problematic on resource-constrained edge)
- **Still requires OADP operator**: ~500 MB memory overhead, doesn't eliminate dependency
- **Not Red Hat supported**: MinIO is community solution, no official support
- **PVC must survive pivot**: Assumes `/var/lib/containers` PVC persists across stateroot switch (same assumption as Option 1)

**Performance Analysis**:

| Scenario | Total IBU Time | Sub-Option 4A (On-Demand) | Sub-Option 4B (Always-On) | Recommendation |
|----------|----------------|---------------------------|---------------------------|----------------|
| **Small workload** (<100 resources, <500 MB backup) | ~10-15 min | +1-2 min = **15-20% slower** ❌ | **+0 min = 0% slower** ✅ | **4B (always-on)** |
| **Medium workload** (100-500 resources, 500 MB-2 GB) | ~20-30 min | +1-2 min = **5-7% slower** ⚠️ | **+0 min = 0% slower** ✅ | **4B (always-on)** |
| **Large workload** (500+ resources, >2 GB backup) | ~45+ min | +1-2 min = **3-4% slower** ✅ | **+0 min = 0% slower** ✅ | **4B (always-on)** |
| **Complex app** (needs backup hooks) | Variable | +1-2 min overhead | **+0 min = 0% slower** ✅ | **4B (always-on)** |
| **Telco SNO** (256GB RAM, speed critical) | Any | +1-2 min = unacceptable | **1GB = 0.4% RAM** ✅ | **4B (always-on)** ⭐ |

**When to Use Sub-Option 4A (On-Demand MinIO)**:
- Resource-constrained edge devices (<32 GB RAM)
- Cannot spare 1 GB continuous memory
- IBU total time >30 minutes (overhead <5%)
- Rare upgrades (MinIO idle most of the time wasteful)

**When to Use Sub-Option 4B (Always-On MinIO)** ⭐ **RECOMMENDED**:
- **Telco SNO hardware** (256GB+ RAM) — 1 GB is trivial (0.4% of RAM)
- **Speed is priority** — 0 seconds overhead preserves IBU speed goals
- **Frequent IBUs** — No deployment overhead every time
- **Simpler implementation** — No lifecycle management code (2-3 days vs 3-5 days)
- **Production deployments** — No risk of MinIO failing to start mid-upgrade

**Resource Requirements**:
```yaml
# MinIO StatefulSet
resources:
  requests:
    memory: "512Mi"  # Minimum to avoid OOM
    cpu: "250m"
  limits:
    memory: "1Gi"    # Headroom for file uploads
    cpu: "500m"
    
# PVC
storage: "10Gi"  # Adjust based on backup size estimates
storageClassName: lvms-vg1  # Local storage (LVMS/LSO)
```

**Implementation Complexity**:
- **Sub-Option 4A (On-Demand)**: Medium (3-5 days)
  - Deploy MinIO StatefulSet: 1 day (YAML + deployment logic)
  - Bucket creation automation: 0.5 day (init Job or exec)
  - OADP configuration: 0.5 day (update DPA to use MinIO endpoint)
  - Lifecycle management: 1 day (deploy on Prep, cleanup after Upgrade)
  - Testing: 1-2 days (E2E with IBU, failure injection)

- **Sub-Option 4B (Always-On)**: **Low (2-3 days)** ⭐
  - Deploy MinIO StatefulSet: 0.5 day (YAML only, no lifecycle code)
  - OADP configuration: 0.5 day (DPA with MinIO endpoint)
  - Documentation: 0.5 day (day-0 deployment guide)
  - Testing: 1 day (E2E with IBU, verify persistence through pivot)
  - **No code changes to LCA controllers** — OADP integration already exists

**Configuration Example**:
```yaml
# MinIO StatefulSet (deployed during Prep stage)
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: minio
  namespace: openshift-lifecycle-agent
spec:
  serviceName: minio
  replicas: 1
  selector:
    matchLabels:
      app: minio
  template:
    metadata:
      labels:
        app: minio
    spec:
      containers:
      - name: minio
        image: quay.io/minio/minio:latest
        args: ["server", "/storage"]
        env:
        - name: MINIO_ROOT_USER
          value: "minioadmin"
        - name: MINIO_ROOT_PASSWORD
          value: "minioadmin"
        - name: GOMEMLIMIT
          value: "900MiB"  # Set Go heap limit to 90% of memory limit
        ports:
        - containerPort: 9000
          name: s3
        volumeMounts:
        - name: storage
          mountPath: /storage
        resources:
          requests:
            memory: "512Mi"
            cpu: "250m"
          limits:
            memory: "1Gi"
            cpu: "500m"
        readinessProbe:
          httpGet:
            path: /minio/health/ready
            port: 9000
          initialDelaySeconds: 10
          periodSeconds: 5
  volumeClaimTemplates:
  - metadata:
      name: storage
    spec:
      accessModes: ["ReadWriteOnce"]
      storageClassName: lvms-vg1
      resources:
        requests:
          storage: 10Gi
---
# Service for MinIO
apiVersion: v1
kind: Service
metadata:
  name: minio
  namespace: openshift-lifecycle-agent
spec:
  selector:
    app: minio
  ports:
  - port: 9000
    targetPort: 9000
    name: s3
---
# OADP DataProtectionApplication (configured to use MinIO)
apiVersion: oadp.openshift.io/v1alpha1
kind: DataProtectionApplication
metadata:
  name: velero
  namespace: openshift-adp
spec:
  configuration:
    velero:
      defaultPlugins: [openshift, aws]
      resourceTimeout: 10m
    restic:
      enable: false  # PV contents persist on local disk
  backupLocations:
    - velero:
        config:
          region: minio
          s3ForcePathStyle: "true"
          s3Url: http://minio.openshift-lifecycle-agent.svc:9000
          insecureSkipTLSVerify: "true"
        provider: aws
        default: true
        credential:
          key: cloud
          name: minio-credentials
        objectStorage:
          bucket: velero
          prefix: lca-backups
---
# MinIO credentials Secret
apiVersion: v1
kind: Secret
metadata:
  name: minio-credentials
  namespace: openshift-adp
type: Opaque
stringData:
  cloud: |
    [default]
    aws_access_key_id = minioadmin
    aws_secret_access_key = minioadmin
```

**Code Changes Required**:
```go
// In prep_handlers.go - PrePivot
func (r *PrepHandler) deployMinIOForBackup(ctx context.Context) error {
    log.Info("Deploying MinIO for local OADP storage")
    
    // 1. Deploy MinIO StatefulSet (~10s)
    if err := r.Client.Create(ctx, minIOStatefulSet); err != nil {
        return fmt.Errorf("failed to create MinIO: %w", err)
    }
    
    // 2. Wait for MinIO ready (~15s)
    if err := wait.PollImmediate(5*time.Second, 30*time.Second, func() (bool, error) {
        pod := &corev1.Pod{}
        if err := r.Client.Get(ctx, types.NamespacedName{
            Name: "minio-0", Namespace: "openshift-lifecycle-agent",
        }, pod); err != nil {
            return false, nil
        }
        return pod.Status.Phase == corev1.PodRunning, nil
    }); err != nil {
        return fmt.Errorf("MinIO not ready: %w", err)
    }
    
    // 3. Create bucket (~5s)
    if err := r.createMinioBucket(ctx, "velero"); err != nil {
        return fmt.Errorf("failed to create bucket: %w", err)
    }
    
    // 4. Configure OADP (~30-60s for DPA reconciliation)
    if err := r.Client.Create(ctx, oadpDataProtectionApp); err != nil {
        return fmt.Errorf("failed to configure OADP: %w", err)
    }
    
    log.Info("MinIO deployed and OADP configured", "elapsed", "~60-90s")
    return nil
}

// In upgrade_handlers.go - PostPivot
func (r *UpgradeHandler) restoreMinIOForRestore(ctx context.Context) error {
    log.Info("Restoring MinIO for OADP restore")
    
    // 1. Verify PVC exists (should survive pivot)
    pvc := &corev1.PersistentVolumeClaim{}
    if err := r.Client.Get(ctx, types.NamespacedName{
        Name: "storage-minio-0", Namespace: "openshift-lifecycle-agent",
    }, pvc); err != nil {
        return fmt.Errorf("MinIO PVC not found (didn't survive pivot?): %w", err)
    }
    
    // 2. Restore MinIO StatefulSet (~10s, fast since PVC exists)
    if err := r.Client.Create(ctx, minIOStatefulSet); err != nil && !errors.IsAlreadyExists(err) {
        return fmt.Errorf("failed to restore MinIO: %w", err)
    }
    
    // 3. Wait for MinIO ready (~15s)
    if err := r.waitForPodReady(ctx, "minio-0", 30*time.Second); err != nil {
        return fmt.Errorf("MinIO not ready: %w", err)
    }
    
    log.Info("MinIO restored, ready for OADP restore", "elapsed", "~25s")
    return nil
}

// After successful restore in Finalize or Idle stage
func (r *UpgradeHandler) cleanupMinIO(ctx context.Context) error {
    log.Info("Cleaning up MinIO after successful restore")
    
    // Delete StatefulSet
    if err := r.Client.Delete(ctx, minIOStatefulSet); err != nil && !errors.IsNotFound(err) {
        return fmt.Errorf("failed to delete MinIO: %w", err)
    }
    
    // Delete PVC (backup data no longer needed)
    if err := r.Client.Delete(ctx, minIOPVC); err != nil && !errors.IsNotFound(err) {
        return fmt.Errorf("failed to delete MinIO PVC: %w", err)
    }
    
    log.Info("MinIO cleanup complete")
    return nil
}
```
**Estimated Lines of Code**:
- **Sub-Option 4A (On-Demand)**: ~400 lines (lifecycle management in LCA controllers)
- **Sub-Option 4B (Always-On)**: **~0 lines** (YAML manifests only, no LCA code changes)

**Trade-offs:**
- **Gain**: Full OADP features, proven reliability, 3-5 days implementation, future S3 migration option
- **Give up**: 1-2 minute startup overhead, OADP operator dependency (500 MB), Red Hat support

**Risk Assessment**:
- **Risk**: MinIO pod fails to start during Prep → **Mitigation**: Retry logic with timeout, fail fast if can't deploy in 2 minutes
- **Risk**: PVC doesn't survive stateroot pivot → **Mitigation**: Test extensively, document PVC persistence requirement (same as Option 1)
- **Risk**: MinIO startup overhead unacceptable for fast IBU → **Mitigation**: Measure actual IBU time, switch to Option 1 if overhead >10%
- **Risk**: MinIO memory OOM during large backups → **Mitigation**: Set GOMEMLIMIT env var, test with max expected backup size, increase limits if needed

**Recommendation**:

**Use Sub-Option 4B (Always-On MinIO)** ⭐ **STRONGLY RECOMMENDED for Telco SNO**:
1. **Telco hardware profile** (256GB+ RAM) — 1 GB is trivial (0.4% overhead)
2. **Speed is critical** — 0 seconds overhead preserves IBU performance goals
3. **Need OADP features** — Backup hooks, CSI snapshots work out of the box
4. **Fastest implementation** — 2-3 days (YAML only, no LCA code changes)
5. **Production-ready** — No risk of MinIO deployment failure during upgrade
6. **Future S3 migration** — Easy switch by updating DPA config

**Use Sub-Option 4A (On-Demand MinIO) IF:**
1. **Resource-constrained edge** (<32 GB RAM) — Cannot spare 1 GB continuous
2. **Rare upgrades** — MinIO idle most of time wasteful
3. **Total IBU time >30 minutes** — 1-2 min overhead <5%, acceptable

**Use Option 1 (Custom Backup) IF:**
1. **Zero dependencies required** — Cannot install OADP operator
2. **Simple backup needs** — No hooks, no CSI, just resource export/import
3. **Willing to invest 9 weeks** for long-term architectural simplicity
4. **Resource-constrained AND speed-critical** — <16 GB RAM AND <15 min IBU (rare scenario)

---

## 3. Decision Matrix

| Criteria | Option 1 (YAML) | Option 2 (Tar) | Option 3 (Hybrid) | Option 4 (MinIO) | Weight | Winner |
|----------|------------------|----------------|-------------------|------------------|--------|--------|
| **Eliminates S3 dependency** | Yes | Yes | No (optional) | Yes (local MinIO) | High | Options 1,2,4 |
| **Eliminates OADP dependency** | Yes | Yes | No (optional) | **No** | High | **Options 1&2** |
| **Disk space requirements** | Low (~500MB) | Low (~500MB) | Medium (dual) | Medium (~1-2GB MinIO+backup) | Medium | Options 1&2 |
| **Implementation complexity** | Low (reuse code) | Medium (tar) | High (dual paths) | **Low (3-5 days)** | High | **Option 4** |
| **Implementation time** | 9 weeks | 9 weeks | 12+ weeks | **3-5 days** | **Critical** | **Option 4** |
| **Restore speed (runtime)** | Fast (0s overhead) | Medium (extract) | Fast (0s overhead) | Fast (-1-2min overhead) | **High** | **Option 1** |
| **OADP features (hooks, CSI)** | **No** | **No** | Yes | **Yes** | Medium | Options 3&4 |
| **Transparency/debuggability** | High (YAML) | Low (archive) | Medium | Medium (S3 API) | Medium | Option 1 |
| **Backward compatibility** | Good (migration) | Good (migration) | Best (no migration) | Good (migration) | High | Option 3 |
| **Maintenance burden** | Low (single path) | Low (single path) | High (dual paths) | Low (OADP existing) | High | Options 1,2,4 |
| **Resource overhead (active)** | ~100MB | ~100MB | ~600MB (OADP) | **512MB-1GB** | Medium | Options 1&2 |
| **Resource overhead (idle)** | 0MB | 0MB | ~500MB (OADP) | **0MB** | Medium | Options 1,2,4 |
| **Air-gap compatibility** | Full | Full | Partial (S3) | Full | High | Options 1,2,4 |
| **Total Score** | **8/10** | **6/10** | **4/10** | **7/10** | - | **Option 1 or 4** |

**Decision Guidance**:

**Choose Option 4B (Always-On MinIO)** ⭐ **RECOMMENDED for Telco SNO**:
- **Best of all worlds**: 0s runtime overhead + full OADP features + 2-3 day implementation
- **Telco hardware** (256GB+ RAM) — 1 GB = 0.4%, trivial cost
- **Speed preserved** — No deployment overhead, same as Option 1
- **Production-ready** — Proven OADP stack, no custom code risk
- **Immediate** — Can deploy today vs 9-week wait for Option 1

**Choose Option 1 (Custom YAML) only if:**
- **Cannot install OADP** — Hard requirement to eliminate dependencies
- **Resource-constrained** — <16 GB RAM available (rare for telco SNO)
- **Long-term architecture** — Willing to invest 9 weeks for simplicity
- Simple backup needs (no hooks/CSI) AND resource constraints

**Choose Option 4A (On-Demand MinIO) only if:**
- **Edge devices** with <32 GB RAM (cannot spare 1 GB continuous)
- **Rare upgrades** where 1 GB idle is wasteful
- IBU >30 minutes (overhead <5%)

**Key Trade-Off Summary**:
- **Option 1**: 9 weeks dev, 0s overhead, minimal features, no dependencies
- **Option 4A**: 3-5 days dev, +1-2min overhead, full features, OADP dependency
- **Option 4B**: **2-3 days dev, 0s overhead, full features, +1GB RAM** ⭐

---

## 4. Design Decisions (Key Choices)

### Decision 1: Local Storage Location

**Options:**

**A: `/var/lib/containers/lca-backups/` (RECOMMENDED)**
- **Pro**: Shared partition across stateroots (`ostreeclient.go:89-107`), survives pivot, 50-200GB available
- **Pro**: Logical grouping with container data, consistent with LCA's container-based architecture
- **Pro**: XFS filesystem optimized for large files and high I/O throughput
- **Con**: Shares space with container images; must monitor disk usage to avoid filling partition

**B: `/var/tmp/lca-backup/`**
- **Pro**: Temporary directory, clear intent for transient data
- **Pro**: Separate from production container storage (no risk of filling container partition)
- **Con**: May be cleaned by system maintenance (`systemd-tmpfiles` with aggressive cleanup)
- **Con**: Smaller partition on some SNO configurations (<10GB)

**C: `/ostree/deploy/rhcos/var/lca-backups/`**
- **Pro**: Under OSTree management, versioned with stateroot
- **Con**: Each stateroot has separate copy (wastes disk space, not shared across pivots)
- **Con**: Backup from stateroot-A not accessible from stateroot-B (breaks restore flow)

**Recommendation**: **Option A (`/var/lib/containers/lca-backups/`)** because `/var/lib/containers` is explicitly designed as a shared partition for persistent data across stateroots. This aligns with LCA's architecture (PV data also lives here) and provides ample disk space. Trade-off: Must implement disk space monitoring to ensure backups don't fill the partition (pre-flight check + auto-cleanup mitigates this).

---

### Decision 2: Backup Format

**Options:**

**A: Individual YAML files per resource type (RECOMMENDED)**
- **Pro**: Human-readable, inspectable with `cat` or `less`, easy debugging
- **Pro**: Reuses existing `export.go` patterns (`internal/clusterconfig/export.go`)
- **Pro**: Parallel apply during restore (can apply namespaces, PVs, PVCs concurrently within waves)
- **Con**: Many small files (1000-5000 for typical SNO workload with 50-100 namespaces)

**B: Tar archive (single `.tar.gz` file)**
- **Pro**: Single file, atomic writes, fewer inodes
- **Con**: Requires extraction before restore (slower), opaque (cannot inspect without unpacking)

**C: JSON Lines (`.jsonl` format)**
- **Pro**: Streaming format (one resource per line), efficient for large datasets
- **Con**: Not human-readable (JSON without pretty-printing), less familiar to Kubernetes admins

**Recommendation**: **Option A (YAML files)** because transparency and debuggability are critical for troubleshooting failed upgrades in production. Administrators can inspect backups with standard tools (`grep`, `cat`) without needing to extract archives. Directory structure: `/var/lib/containers/lca-backups/<ibu-name>-<timestamp>/namespaces.yaml`, `pvs.yaml`, `pvcs.yaml`, etc. (one file per resource type, with multiple resources in each file separated by `---`). Trade-off: More files (but XFS handles this well), gain debuggability.

---

### Decision 3: Restore Orchestration

**Options:**

**A: Sequential wave-based apply (existing logic) (RECOMMENDED)**
- **Pro**: Reuses proven logic from `restore.go:216-390` (namespaces → PVs → PVCs → Deployments → ...)
- **Pro**: Respects resource dependencies (namespaces must exist before PVCs)
- **Pro**: Simple error handling (stop on first failure, rollback applied resources)
- **Con**: Slower than parallel apply (but restore time is not critical for IBU use case)

**B: Parallel apply with dependency graph**
- **Pro**: Faster restore (apply independent resources concurrently)
- **Con**: Complex dependency tracking (must parse ownerReferences, namespace membership)
- **Con**: Higher risk of race conditions (PVC applied before namespace exists)

**C: Controller-based reconciliation (Kubernetes operator pattern)**
- **Pro**: Automatic retry on failure, eventual consistency
- **Con**: Massive over-engineering for one-time restore operation
- **Con**: Requires writing custom controller, CRDs for restore state

**Recommendation**: **Option A (wave-based sequential apply)** because restore happens once per upgrade (not a hot path), and simplicity/reliability outweigh speed. Existing `restore.go` wave order is battle-tested:
1. Namespaces
2. PVs (cluster-scoped)
3. PVCs (namespace-scoped, depend on PVs)
4. ConfigMaps, Secrets
5. Deployments, StatefulSets, DaemonSets
6. Services, Routes

Trade-off: Slower restore (30-60s for typical workload vs 10-20s parallel), gain simplicity and proven reliability.

---

### Decision 4: Backup Versioning and Cleanup

**Options:**

**A: Timestamp-based directories with auto-cleanup (RECOMMENDED)**
- **Pro**: Each backup is immutable, easy to identify by timestamp
- **Pro**: Auto-cleanup keeps last N backups (configurable, default 3) to limit disk usage
- **Con**: Requires cleanup logic (purge old backups before creating new one)

**B: Single backup directory (overwrite on each backup)**
- **Pro**: Simplest implementation, no cleanup needed
- **Con**: No history, cannot rollback to previous backup if current is corrupt

**C: Git-based versioning**
- **Pro**: Full history with diffs, branching for experimental restores
- **Con**: Massive over-engineering, requires Git in LCA container, slow for large resource sets

**Recommendation**: **Option A (timestamp-based with cleanup)** because it provides safety (keep last 3 backups in case of corruption) without unbounded disk growth. Directory naming: `/var/lib/containers/lca-backups/<ibu-name>-<timestamp>/` where `<timestamp>` is RFC3339 format (e.g., `upgrade-2026-06-25T14-30-00Z`). Cleanup policy: Before creating new backup, delete directories older than the 2 most recent. Trade-off: Requires ~2-3GB disk space (3 backups × ~1GB each), gain resilience against backup corruption.

---

### Decision 5: Error Handling and Rollback

**Options:**

**A: Transactional restore with rollback (RECOMMENDED)**
- **Pro**: Track all applied resources, delete them if restore fails (returns cluster to pre-restore state)
- **Pro**: Aligns with IBU's rollback semantics (if upgrade fails, revert to previous stateroot)
- **Con**: Requires tracking applied resources (in-memory or persisted to disk)

**B: Best-effort restore (apply as much as possible, ignore errors)**
- **Pro**: Simple implementation, no rollback logic
- **Con**: Leaves cluster in undefined state on failure (partial restore)

**C: Dry-run validation before apply**
- **Pro**: Catch errors (invalid YAMLs, missing CRDs) before modifying cluster
- **Con**: Dry-run doesn't catch all failures (e.g., webhook rejections, resource conflicts)

**Recommendation**: **Option A + C (transactional with dry-run pre-check)** because IBU upgrades are critical operations; partial restore is unacceptable. Implementation:
1. **Dry-run phase**: `kubectl apply --dry-run=server` all resources, abort if any fail
2. **Apply phase**: Apply resources wave-by-wave, track applied resources in ConfigMap (`lca-restore-state`)
3. **Rollback on failure**: Delete all resources in `lca-restore-state` (in reverse order)

Trade-off: Slower restore (dry-run adds 10-20% overhead), gain atomicity and safety.

---

### Decision 6: Backup Content Specification API

**Challenge**: How do users specify what resources to backup when switching from OADP to local implementation? Current approach uses `spec.oadpContent` field (array of ConfigMapRefs) pointing to ConfigMaps containing Velero Backup CRs.

**Options:**

**A: Reinterpret Existing `oadpContent` Field (RECOMMENDED)**
- **Approach**: Keep `spec.oadpContent` field unchanged. Local backup implementation interprets the Velero Backup CR specs (from ConfigMaps) directly instead of creating OADP Backup CRs. The `spec.backupMode` field switches between backends, not API structure.
- **Pro**: **Zero breaking changes** — existing IBU CRs and ConfigMaps work as-is with both OADP and local modes
- **Pro**: Reuses existing code patterns (`GetSortedBackupsFromConfigmap()` from backup.go:58-87, wave-based ordering)
- **Pro**: Users already familiar with ConfigMap + Velero Backup CR pattern; no re-learning needed
- **Pro**: Smooth migration — just change `backupMode: oadp` to `backupMode: local`, no ConfigMap updates required
- **Pro**: Wave ordering preserved (`lca.openshift.io/apply-wave` annotation still works)
- **Con**: ConfigMaps still reference "velero.io/v1/Backup" kind (confusing when OADP is gone, but functional)
- **Con**: Tied to Velero's API schema (`includedNamespaces`, `includedClusterScopedResources`, etc.)
- **Con**: Cannot easily add local-specific features (compression, encryption flags) without extending Velero schema

**Example Usage** (unchanged from current OADP approach):
```yaml
# User's ConfigMap (no changes needed)
apiVersion: v1
kind: ConfigMap
metadata:
  name: oadp-cm
  namespace: openshift-adp
data:
  backup.yaml: |
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: acm-klusterlet
      annotations:
        lca.openshift.io/apply-wave: "1"
    spec:
      includedNamespaces:
        - open-cluster-management-agent
      includedClusterScopedResources:
        - clusterroles.rbac.authorization.k8s.io
        - clusterrolebindings.rbac.authorization.k8s.io

# IBU CR (only backupMode added)
apiVersion: lca.openshift.io/v1
kind: ImageBasedUpgrade
metadata:
  name: upgrade
spec:
  stage: Upgrade
  backupMode: local  # NEW: switches to local backend
  oadpContent:       # UNCHANGED: same field, same ConfigMaps
    - name: oadp-cm
      namespace: openshift-adp
  seedImageRef:
    image: quay.io/seed:4.16
    version: 4.16.0
```

**Implementation** (`internal/backuprestore/localbackup.go`):
```go
func (h *LocalBackupHandler) CreateLocalBackup(ctx context.Context, oadpContent []ConfigMapRef) error {
    // Extract Velero Backup CRs from configmaps (reuse existing function)
    backupCRs, err := common.ExtractResourcesFromConfigmaps[*velerov1.Backup](configmaps, common.BackupGvk)
    
    // Interpret Backup CR specs to determine what to backup locally (not create OADP CRs)
    for _, backup := range backupCRs {
        // Use includedNamespaces to filter
        for _, ns := range backup.Spec.IncludedNamespaces {
            h.exportNamespaceResources(ctx, ns, backupDir)
        }
        // Use includedClusterScopedResources to filter
        for _, resource := range backup.Spec.IncludedClusterScopedResources {
            h.exportClusterScopedResource(ctx, resource, backupDir)
        }
        // Respect apply-wave annotation for restore ordering
        wave := backup.Annotations["lca.openshift.io/apply-wave"]
    }
}
```

**Trade-offs:**
- **Gain**: Zero migration effort, backward compatibility, reuse proven patterns, fast implementation
- **Give up**: API purity (Velero references remain), flexibility for local-specific features

---

**B: New `backupContent` Field with LCA-Native BackupSpec**
- **Approach**: Add new `spec.backupContent` field pointing to ConfigMaps with LCA-native `BackupSpec` CRs (not Velero). Deprecate `oadpContent` over 2-3 releases.
- **Pro**: Clean API — no Velero dependencies in schema
- **Pro**: Can add LCA-specific features (compression, encryption, retention policies) without schema conflicts
- **Pro**: Clear separation between OADP (deprecated) and local (current) modes
- **Con**: **Breaking change** — users must update ConfigMaps with new CR format
- **Con**: Dual-mode support complexity during deprecation window (support both `oadpContent` and `backupContent`)
- **Con**: More migration work for existing users (rewrite ConfigMaps)
- **Con**: Longer implementation timeline (design new CRD, validation, tooling)

**Example Usage**:
```yaml
# NEW ConfigMap format with LCA-native BackupSpec
apiVersion: v1
kind: ConfigMap
metadata:
  name: lca-backup-cm
  namespace: openshift-lifecycle-agent
data:
  platform-backup.yaml: |
    apiVersion: lca.openshift.io/v1
    kind: BackupSpec
    metadata:
      name: acm-klusterlet
      annotations:
        lca.openshift.io/apply-wave: "1"
    spec:
      includedNamespaces:
        - open-cluster-management-agent
      includedClusterScopedResources:
        - group: rbac.authorization.k8s.io
          version: v1
          resource: clusterroles
        - group: rbac.authorization.k8s.io
          version: v1
          resource: clusterrolebindings
      # NEW: local-specific options
      compression: gzip
      encryption: false

# IBU CR with NEW field
apiVersion: lca.openshift.io/v1
kind: ImageBasedUpgrade
metadata:
  name: upgrade
spec:
  stage: Upgrade
  backupMode: local
  backupContent:  # NEW field (replaces oadpContent)
    - name: lca-backup-cm
      namespace: openshift-lifecycle-agent
  seedImageRef:
    image: quay.io/seed:4.16
```

**API Changes** (`api/imagebasedupgrade/v1/types.go`):
```go
type ImageBasedUpgradeSpec struct {
    Stage ImageBasedUpgradeStage `json:"stage,omitempty"`
    SeedImageRef SeedImageRef `json:"seedImageRef,omitempty"`
    
    // DEPRECATED: Use backupContent instead
    // +optional
    OADPContent []ConfigMapRef `json:"oadpContent,omitempty"`
    
    // BackupContent defines backup specifications using LCA-native BackupSpec CRs
    // +optional
    BackupContent []ConfigMapRef `json:"backupContent,omitempty"`
    
    // +kubebuilder:validation:Enum=local;oadp
    // +kubebuilder:default=local
    BackupMode string `json:"backupMode,omitempty"`
    
    ExtraManifests []ConfigMapRef `json:"extraManifests,omitempty"`
    AutoRollbackOnFailure *AutoRollbackOnFailure `json:"autoRollbackOnFailure,omitempty"`
}

// BackupSpec defines LCA-native backup specification
type BackupSpec struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec BackupSpecSpec `json:"spec,omitempty"`
}

type BackupSpecSpec struct {
    IncludedNamespaces []string `json:"includedNamespaces,omitempty"`
    ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`
    IncludedClusterScopedResources []ResourceIdentifier `json:"includedClusterScopedResources,omitempty"`
    ExcludedResources []string `json:"excludedResources,omitempty"`
    // Local-specific options
    Compression string `json:"compression,omitempty"` // none, gzip, zstd
    Encryption bool `json:"encryption,omitempty"`
}

type ResourceIdentifier struct {
    Group string `json:"group,omitempty"`
    Version string `json:"version"`
    Resource string `json:"resource"`
}
```

**Migration Timeline**:
- **v1.0-v1.2** (12 weeks): Dual support — both `oadpContent` and `backupContent` work
- **v1.3** (week 13+): Deprecation warning when `oadpContent` used
- **v2.0** (6 months): Remove `oadpContent`, only `backupContent` supported

**Trade-offs:**
- **Gain**: API purity, flexibility for future features, clear deprecation path
- **Give up**: Backward compatibility (requires migration), faster time to value

---

**C: Inline Backup Specification in IBU CR**
- **Approach**: Define backup specs directly in IBU CR without ConfigMaps.
- **Pro**: Simpler for users (no ConfigMap management)
- **Pro**: Easier validation (CEL rules in CRD)
- **Pro**: GitOps-friendly (single CR captures all config)
- **Con**: IBU CR can become very large for complex backups (multiple namespaces, many resource types)
- **Con**: Harder to share backup specs across clusters (ConfigMaps are reusable)
- **Con**: CRD size limits (etcd 1.5MB per object) — may hit limits for large backup specs
- **Con**: Users lose flexibility to organize backups in multiple ConfigMaps

**Example Usage**:
```yaml
apiVersion: lca.openshift.io/v1
kind: ImageBasedUpgrade
metadata:
  name: upgrade
spec:
  stage: Upgrade
  backupMode: local
  # Inline backup specifications (no ConfigMaps)
  backups:
    - name: acm-klusterlet
      wave: 1
      includedNamespaces:
        - open-cluster-management-agent
      includedClusterScopedResources:
        - group: rbac.authorization.k8s.io
          version: v1
          resource: clusterroles
    - name: lvms-config
      wave: 2
      includedNamespaces:
        - openshift-storage
      includedNamespaceScopedResources:
        - lvmclusters
        - lvmvolumegroups
  seedImageRef:
    image: quay.io/seed:4.16
```

**API Changes**:
```go
type ImageBasedUpgradeSpec struct {
    Stage ImageBasedUpgradeStage `json:"stage,omitempty"`
    SeedImageRef SeedImageRef `json:"seedImageRef,omitempty"`
    
    // Inline backup specifications (replaces oadpContent)
    // +optional
    Backups []BackupSpec `json:"backups,omitempty"`
    
    // +kubebuilder:validation:Enum=local;oadp
    // +kubebuilder:default=local
    BackupMode string `json:"backupMode,omitempty"`
    
    ExtraManifests []ConfigMapRef `json:"extraManifests,omitempty"`
    AutoRollbackOnFailure *AutoRollbackOnFailure `json:"autoRollbackOnFailure,omitempty"`
}

type BackupSpec struct {
    Name string `json:"name"`
    Wave int `json:"wave,omitempty"`
    IncludedNamespaces []string `json:"includedNamespaces,omitempty"`
    ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`
    IncludedClusterScopedResources []string `json:"includedClusterScopedResources,omitempty"`
    IncludedNamespaceScopedResources []string `json:"includedNamespaceScopedResources,omitempty"`
    ExcludedResources []string `json:"excludedResources,omitempty"`
}
```

**Trade-offs:**
- **Gain**: Simplicity (no ConfigMaps), strong validation, GitOps alignment
- **Give up**: Configurability (limited by etcd size), reusability across clusters

---

**Recommendation**: **Option A (Reinterpret `oadpContent`)** for initial implementation (Phases 1-4, weeks 1-9).

**Rationale**:
1. **Aligns with strategic plan goals**: "Backward compatibility via feature flag" (line 328), "Preserve resource filtering logic" (line 385)
2. **Zero breaking changes**: Existing users switch mode without updating ConfigMaps
3. **Fastest time to value**: No ConfigMap migration overhead, no new CRD design/testing
4. **Reuses proven patterns**: Wave-based ordering, resource filtering already battle-tested with OADP
5. **Acceptable trade-off**: API purity (Velero references) is less important than smooth migration and fast implementation

**Future Path** (Optional, post-v1.0):
- **v1.1 or v2.0**: Introduce Option B (`backupContent` with LCA-native BackupSpec) if users request local-specific features (compression, encryption) or cleaner API
- **Deprecation timeline**: 2-3 releases to migrate from `oadpContent` to `backupContent`, similar to OADP→local migration strategy

---

## 5. Recommended Solution

### 5.1 High-Level Architecture

The recommended solution replaces OADP's S3-based backup/restore with a local Kubernetes-native approach:

**Backup (PrePivot)**: When IBU stage transitions to `Prep`, the LCA controller (`controllers/upgrade_handlers.go:146`) serializes all Kubernetes resources (filtered by namespace/label) to YAML manifest files stored in `/var/lib/containers/lca-backups/<ibu-name>-<timestamp>/`. This directory is on the shared `/var/lib/containers` partition, which persists across stateroot pivots. Resources are grouped by type (namespaces, PVs, PVCs, Deployments, etc.) to facilitate wave-based restore. PV contents (actual data in `/var/lib/containers/storage/`) are not backed up; they persist on the shared partition automatically.

**Restore (PostPivot)**: After pivoting to the new stateroot, the LCA controller (`controllers/upgrade_handlers.go:438`) reads YAML manifests from the backup directory and applies them to the cluster in dependency order (namespaces first, then PVs, PVCs, ConfigMaps, workloads). Restore is transactional: dry-run validation occurs before apply, and all applied resources are tracked so they can be rolled back if the restore fails. This ensures the cluster returns to a consistent state even if the upgrade is aborted.

**Implementation Scope**: New backup/restore logic will live in `internal/backuprestore/localbackup.go` and `localrestore.go`, reusing resource serialization patterns from `internal/clusterconfig/export.go`. Existing OADP integration code (`backup.go:337-428` for DPA/secret export, `restore.go:95-150` for OADP CR creation) will be removed. The wave-based restore logic from `restore.go:216-390` will be preserved and adapted for local YAML apply.

**Backward Compatibility**: Existing IBU clusters using OADP will detect the new local backup capability via a feature flag in the IBU CR (`spec.backupMode: local|oadp`). Default is `local` for new installs; existing clusters remain on `oadp` until explicitly migrated. A migration procedure (documented in Phase 4) allows users to switch from OADP to local backup without disrupting active upgrades.

---

### 5.2 Component Changes

**New Components:**

- **`internal/backuprestore/localbackup.go`**: 
  - **Responsibility**: Export Kubernetes resources to YAML files in `/var/lib/containers/lca-backups/<timestamp>/`
  - **Key Functions**:
    - `CreateLocalBackup(ctx, client, backupDir, filters)`: Main entry point, orchestrates resource export
    - `exportResourceType(ctx, client, gvk, outputFile)`: Serialize resources of given type to YAML
    - `cleanupOldBackups(backupBaseDir, keepCount)`: Purge old backup directories, keep last N
  - **Dependencies**: Reuses `internal/clusterconfig/export.go` serialization patterns, `client-go` dynamic client

- **`internal/backuprestore/localrestore.go`**:
  - **Responsibility**: Restore Kubernetes resources from YAML files in `/var/lib/containers/lca-backups/<timestamp>/`
  - **Key Functions**:
    - `RestoreFromLocal(ctx, client, backupDir)`: Main entry point, orchestrates wave-based restore
    - `applyResourceWave(ctx, client, yamlFiles, wave)`: Apply resources in given wave (e.g., namespaces, PVs)
    - `dryRunValidation(ctx, client, yamlFiles)`: Pre-check all YAMLs before applying
    - `trackAppliedResources(ctx, client, resources)`: Persist applied resource list to ConfigMap for rollback
    - `rollbackRestore(ctx, client, stateConfigMap)`: Delete all applied resources in reverse order
  - **Dependencies**: `kubectl apply` logic via `client-go`, wave ordering from existing `restore.go`

- **`internal/backuprestore/common.go`** (enhanced):
  - **Additions**: Constants for backup directory paths, wave definitions (move from `restore.go`)
  - **Functions**: Disk space checking (`ensureSufficientDiskSpace(path, requiredGB)`), checksum validation

**Modified Components:**

- **`controllers/upgrade_handlers.go:146` (`HandleBackup()` in Prep stage)**:
  - **Change**: Replace OADP backup logic with call to `localbackup.CreateLocalBackup()`
  - **Before**: Exports DPA/secrets to ConfigMaps, creates OADP Backup CR, waits for completion
  - **After**: Creates local backup directory, exports resources to YAML, validates backup integrity
  - **Lines affected**: ~50 lines (backup.go:337-428 DPA export logic removed, replaced with ~20 lines for local backup)

- **`controllers/upgrade_handlers.go:438` (`HandleRestore()` in Upgrade stage)**:
  - **Change**: Replace OADP restore logic with call to `localrestore.RestoreFromLocal()`
  - **Before**: Imports DPA/secrets from ConfigMaps, creates OADP Restore CR, waits for wave completion
  - **After**: Reads YAML files from backup directory, applies in waves with dry-run + transactional rollback
  - **Lines affected**: ~80 lines (restore.go:95-150 OADP CR creation removed, replaced with ~40 lines for local restore)

- **`api/imagebasedupgrade/v1/imagebasedupgrade_types.go`**:
  - **Change**: Add `BackupMode` field to IBU spec for backward compatibility
  - **New Field**: 
    ```go
    // BackupMode specifies the backup strategy: "local" (default) or "oadp" (deprecated)
    // +kubebuilder:validation:Enum=local;oadp
    // +kubebuilder:default=local
    BackupMode string `json:"backupMode,omitempty"`
    ```
  - **Lines affected**: ~5 lines (new field + validation)

- **`internal/backuprestore/backup.go`** (partially removed):
  - **Removed**: Lines 337-428 (DPA/secret export to ConfigMaps)
  - **Preserved**: Lines 144-213 (resource filtering logic, can be reused for local backup)
  - **Outcome**: File shrinks from ~430 lines to ~300 lines

- **`internal/backuprestore/restore.go`** (refactored):
  - **Removed**: Lines 95-150 (OADP Restore CR creation, DPA import)
  - **Preserved**: Lines 216-390 (wave definitions, resource ordering logic)
  - **Moved**: Wave logic extracted to `common.go` for reuse by `localrestore.go`
  - **Outcome**: File refactored, OADP-specific code removed, wave logic becomes shared constant

**Removed Components:**

- **OADP operator dependency** (`bundle/manifests/dependencies.yaml` or equivalent)
- **S3 backend configuration** (`internal/backuprestore/backup.go:337-428`, DPA export logic)
- **Velero-specific constants** (`internal/common/constants.go`, e.g., `VeleroNamespace`, `VeleroBackupLabel`)

**Estimated Net Lines of Code**:
- **Removed**: ~200 lines (DPA export, OADP CR creation, S3 config)
- **Added**: ~400 lines (localbackup.go ~200, localrestore.go ~200)
- **Net change**: +200 lines (but simpler, no external dependencies)

---

### 5.3 Data Flow

**Backup Flow (PrePivot - Prep Stage):**

1. **Trigger** (`controllers/upgrade_handlers.go:146`, `HandleBackup()`):
   - IBU CR stage set to `Prep` by user
   - Controller detects stage change, initiates backup

2. **Pre-flight Checks** (`localbackup.go`, `CreateLocalBackup()`):
   - Check `/var/lib/containers` has >10GB free space (call `ensureSufficientDiskSpace()`)
   - Purge old backups, keeping last 2 (`cleanupOldBackups()`)
   - Create new backup directory: `/var/lib/containers/lca-backups/upgrade-<timestamp>/`

3. **Resource Export** (`localbackup.go`, `exportResourceType()` loop):
   - Query Kubernetes API for each resource type (Namespaces, PVs, PVCs, ConfigMaps, Secrets, Deployments, StatefulSets, DaemonSets, Services, Routes, etc.)
   - Apply filters (exclude `kube-system`, `openshift-*` system namespaces; include user workloads)
   - Serialize each resource to YAML using `client-go` serializer (reuses `export.go` pattern)
   - Write to file: `<backupDir>/namespaces.yaml`, `<backupDir>/pvs.yaml`, etc.
   - Resources within each file separated by `---\n` (standard YAML multi-document format)

4. **Post-backup Validation**:
   - Generate SHA256 checksum manifest: `<backupDir>/checksums.txt` (one line per file: `<hash> <filename>`)
   - Write backup metadata: `<backupDir>/metadata.json` (IBU name, timestamp, Kubernetes version, backup size)
   - Update IBU CR status: `status.backupPath: /var/lib/containers/lca-backups/upgrade-<timestamp>`

5. **Storage Location**:
   - Files written to `/var/lib/containers` partition (shared across stateroots per `ostreeclient.go:89-107`)
   - Typical size: 200-500MB uncompressed, 50-100MB with gzip (if compression added in future)

---

**Restore Flow (PostPivot - Upgrade Stage):**

1. **Trigger** (`controllers/upgrade_handlers.go:438`, `HandleRestore()`):
   - Node reboots into new stateroot after pivot
   - LCA controller starts, detects IBU CR stage is `Upgrade`
   - Reads `status.backupPath` from IBU CR to locate backup directory

2. **Pre-restore Validation** (`localrestore.go`, `RestoreFromLocal()`):
   - Verify backup directory exists on `/var/lib/containers` (accessible from new stateroot)
   - Validate checksums: read `checksums.txt`, verify SHA256 of each YAML file
   - Ensure Kubernetes API server is ready (retry with exponential backoff up to 5 minutes)

3. **Dry-Run Phase** (`localrestore.go`, `dryRunValidation()`):
   - Parse all YAML files, build resource list
   - Apply each resource with `kubectl apply --dry-run=server` (validates against API server without modifying cluster)
   - Abort if any resource fails dry-run (missing CRD, invalid spec, etc.)
   - Log validation results to IBU CR status

4. **Wave-Based Apply** (`localrestore.go`, `applyResourceWave()` loop):
   - Apply resources in dependency order (waves from `restore.go:216-390`):
     - **Wave 1**: Namespaces (`namespaces.yaml`)
     - **Wave 2**: PVs (`pvs.yaml`)
     - **Wave 3**: PVCs (`pvcs.yaml`) — wait for PVs to be `Available`
     - **Wave 4**: ConfigMaps, Secrets (`configmaps.yaml`, `secrets.yaml`)
     - **Wave 5**: Deployments, StatefulSets, DaemonSets (`deployments.yaml`, ...)
     - **Wave 6**: Services, Routes (`services.yaml`, `routes.yaml`)
   - For each resource: `kubectl apply` via `client-go`, retry up to 3 times on transient errors
   - Track applied resources in ConfigMap (`lca-restore-state` in `openshift-lifecycle-agent` namespace)

5. **Post-restore Validation**:
   - Wait for critical pods to be `Running` (e.g., CNI, DNS) — timeout 10 minutes
   - Verify PVCs are `Bound` to PVs (PV data already on disk from `/var/lib/containers`)
   - Update IBU CR status: `status.restoreComplete: true`, `status.restoredResources: <count>`

6. **Rollback on Failure** (`localrestore.go`, `rollbackRestore()`):
   - If any wave fails (apply errors, timeout waiting for pods):
     - Read `lca-restore-state` ConfigMap, get list of applied resources
     - Delete resources in reverse order (Services → Deployments → ConfigMaps → PVCs → PVs → Namespaces)
     - Update IBU CR status: `status.restoreComplete: false`, `status.error: "Restore failed at wave N, rolled back"`
   - Cluster returns to state before restore (empty, ready for retry or manual intervention)

7. **Cleanup**:
   - After successful restore, delete `lca-restore-state` ConfigMap (no longer needed)
   - Optionally compress backup directory (gzip) to save space, or leave for debugging

---

### 5.4 Backward Compatibility Strategy

**Challenge**: Existing IBU clusters have OADP operator installed and configured; they expect OADP-based backup/restore. Switching to local backup mid-upgrade could break active workflows.

**Solution**: Feature flag + migration path

**Phase 1 - Dual Mode Support (Weeks 1-4)**:
- Add `spec.backupMode` field to IBU CR (default `local` for new clusters, `oadp` for existing)
- Keep existing OADP code (`backup.go`, `restore.go`) alongside new local code (`localbackup.go`, `localrestore.go`)
- Controller dispatches to local or OADP handlers based on `backupMode` field

**Phase 2 - Deprecation Warning (Weeks 5-7)**:
- Log deprecation warning when `backupMode: oadp` is used: "OADP backup mode is deprecated, will be removed in LCA v2.0. Migrate to local backup."
- Document migration procedure in `docs/` directory (see below)
- Update IBU CR status to show deprecation: `status.warnings: ["OADP mode deprecated"]`

**Phase 3 - Migration Period (Weeks 8-16, 2 releases)**:
- Release LCA v1.X with dual mode support
- Give users 2 release cycles (6-8 weeks) to migrate
- During this period, both modes are supported, tested, documented

**Phase 4 - OADP Removal (Week 17+, LCA v2.0)**:
- Remove OADP code (`backup.go:337-428`, `restore.go:95-150`, DPA/secret logic)
- Remove `spec.backupMode` field (always use local)
- Remove OADP operator from bundle dependencies

**Migration Procedure** (to be documented in `docs/migrate-oadp-to-local.md`):

1. **Before Upgrade**: Ensure cluster is in `Idle` stage, no active IBU in progress
2. **Backup OADP Config** (optional, for rollback): Export DPA CR, S3 secret to YAML files
3. **Update IBU CR**: 
   ```bash
   oc patch imagebasedupgrade upgrade -n openshift-lifecycle-agent \
     --type=merge -p '{"spec":{"backupMode":"local"}}'
   ```
4. **Test Backup**: Transition IBU to `Prep` stage, verify local backup created in `/var/lib/containers/lca-backups/`
5. **Test Restore**: Transition to `Upgrade` stage, verify resources restored from local backup
6. **Uninstall OADP** (after successful restore): 
   ```bash
   oc delete dataprotectionapplication velero-dpa -n openshift-oadp
   oc delete subscription oadp-operator -n openshift-oadp
   ```

**Rollback Plan** (if migration fails):
- Revert IBU CR: `oc patch imagebasedupgrade upgrade ... -p '{"spec":{"backupMode":"oadp"}}'`
- Reinstall OADP operator, restore DPA CR
- Continue using OADP until issue resolved

**Validation**:
- E2E test for OADP→local migration (simulate existing cluster, change `backupMode`, verify upgrade succeeds)
- CI test matrix: both `backupMode: local` and `backupMode: oadp` paths tested in parallel

---

## 6. Implementation Roadmap

### Phase 1: Foundation (Weeks 1-2)

**Goal**: Implement core local backup/restore logic, validate on test cluster

- **Task 1.1**: Implement `internal/backuprestore/localbackup.go`
  - **Input**: Kubernetes client, `spec.oadpContent` ConfigMapRefs (containing Velero Backup CRs), backup directory path
  - **Output**: YAML files in `/var/lib/containers/lca-backups/<timestamp>/`, checksum manifest
  - **Key Functions**: 
    - `CreateLocalBackup(ctx, client, oadpContent, backupDir)`: Main entry point
    - `parseBackupSpecsFromConfigMaps(oadpContent)`: Extract Velero Backup CRs, interpret `.spec` for resource filtering (reuses `GetSortedBackupsFromConfigmap()` from backup.go:58-87)
    - `exportResourceType(ctx, client, gvk, filters, outputFile)`: Serialize filtered resources to YAML
    - `cleanupOldBackups(backupBaseDir, keepCount)`: Purge old backups
  - **Resource Filtering**: Interpret Velero Backup CR fields (`includedNamespaces`, `includedClusterScopedResources`, `excludedResources`, etc.) to determine what to export locally. Respect `lca.openshift.io/apply-wave` annotations for restore ordering.
  - **Testing**: Unit tests with fake client, verify YAML serialization matches expected format. Test with sample ConfigMaps containing Velero Backup CRs.
  - **Risk**: Disk space exhaustion → **Mitigation**: Pre-flight check for 10GB free, fail fast if insufficient space
  - **Owner**: Engineer A
  - **Deliverable**: Working `localbackup.go` with unit tests, code review complete

- **Task 1.2**: Implement `internal/backuprestore/localrestore.go`
  - **Input**: Kubernetes client, backup directory path
  - **Output**: Resources applied to cluster in wave order, `lca-restore-state` ConfigMap updated
  - **Key Functions**: `RestoreFromLocal()`, `applyResourceWave()`, `dryRunValidation()`, `rollbackRestore()`
  - **Testing**: Unit tests with fake client, integration test with kind cluster (backup→restore round-trip)
  - **Risk**: Resource conflicts (existing resources with same name) → **Mitigation**: Dry-run detects conflicts, log clear error, abort restore
  - **Owner**: Engineer A
  - **Deliverable**: Working `localrestore.go` with unit tests, integration test passing

- **Task 1.3**: Disk space monitoring utility
  - **Function**: `ensureSufficientDiskSpace(path string, requiredGB int) error` in `common.go`
  - **Logic**: Call `syscall.Statfs()` on `/var/lib/containers`, check `Bavail * Bsize >= requiredGB * 1GB`
  - **Testing**: Unit test with mock filesystem, verify error returned when space insufficient
  - **Owner**: Engineer B
  - **Deliverable**: Utility function with unit test

---

### Phase 2: Integration (Weeks 3-4)

**Goal**: Integrate local backup/restore into IBU controller, add feature flag for backward compatibility

- **Task 2.1**: Update IBU CR API with `backupMode` field
  - **File**: `api/imagebasedupgrade/v1/imagebasedupgrade_types.go`
  - **Change**: Add `BackupMode string` field to `ImageBasedUpgradeSpec`, validation enum `local|oadp`, default `local`
  - **API Design**: Keep existing `OADPContent []ConfigMapRef` field unchanged (Decision 6, Option A). Local mode reinterprets Velero Backup CR specs from ConfigMaps without creating OADP CRs.
  - **Code Generation**: Run `make generate manifests` to update CRD YAML
  - **Testing**: Apply updated CRD to test cluster, verify `backupMode` field accepted/validated. Test with existing ConfigMaps (no changes needed).
  - **Risk**: CRD upgrade breaks existing IBU CRs → **Mitigation**: Field is optional, defaults to `oadp` for existing CRs (backward compatible)
  - **Owner**: Engineer B
  - **Deliverable**: Updated CRD, `make manifests` output verified, existing ConfigMaps validated with local mode

- **Task 2.2**: Refactor `controllers/upgrade_handlers.go:146` (`HandleBackup()`)
  - **Change**: Add conditional logic:
    ```go
    if ibu.Spec.BackupMode == "local" {
        return localbackup.CreateLocalBackup(ctx, r.Client, backupDir, filters)
    } else {
        // Existing OADP logic (backup.go:337-428)
        return createOADPBackup(ctx, r.Client, ibu)
    }
    ```
  - **Testing**: E2E test with `backupMode: local`, verify local backup created; E2E test with `backupMode: oadp`, verify OADP backup still works
  - **Risk**: Breaking existing users → **Mitigation**: Default to `oadp` for clusters without `backupMode` field (detect nil, set default)
  - **Owner**: Engineer A
  - **Deliverable**: Updated `upgrade_handlers.go`, E2E tests passing for both modes

- **Task 2.3**: Refactor `controllers/upgrade_handlers.go:438` (`HandleRestore()`)
  - **Change**: Add conditional logic similar to Task 2.2, dispatch to `localrestore.RestoreFromLocal()` or existing OADP restore
  - **Testing**: E2E test for local restore (backup in Prep, restore in Upgrade), verify resources applied; E2E test for OADP restore (backward compat)
  - **Risk**: Restore failure mid-upgrade → **Mitigation**: Transactional rollback (Task 1.2), E2E test simulates failures (kill API server mid-restore, verify rollback)
  - **Owner**: Engineer A
  - **Deliverable**: Updated `upgrade_handlers.go`, E2E tests for both modes including failure scenarios

- **Task 2.4**: Add deprecation warning for OADP mode
  - **File**: `controllers/upgrade_handlers.go` (in `Reconcile()` function)
  - **Change**: Log warning when `backupMode == "oadp"`, update IBU CR status: `status.warnings = append(..., "OADP backup mode is deprecated...")`
  - **Testing**: Verify warning appears in controller logs and IBU CR status when `backupMode: oadp`
  - **Owner**: Engineer B
  - **Deliverable**: Warning logged and visible in `oc describe imagebasedupgrade`

---

### Phase 3: Testing & Validation (Weeks 5-6)

**Goal**: Comprehensive testing across IBU scenarios, performance benchmarking, failure injection

- **Task 3.1**: E2E IBU test with local backup/restore
  - **Scenario**: Fresh SNO cluster, deploy sample workload (Deployment + PVC), run IBU Prep → Upgrade → rollback to original stateroot
  - **Validation**: 
    - Backup created in `/var/lib/containers/lca-backups/`
    - After pivot, workload restored (Deployment running, PVC bound to PV, data accessible)
    - Rollback to original stateroot, workload still intact
  - **Environment**: OpenShift CI test cluster (SNO 4.18)
  - **Owner**: QE team / Engineer B
  - **Deliverable**: Passing E2E test added to CI pipeline

- **Task 3.2**: Upgrade path testing (OADP → local migration)
  - **Scenario**: 
    - Cluster running LCA v1.X with OADP backup (`backupMode: oadp`)
    - Upgrade to LCA v1.Y with local backup support
    - Change `backupMode: local`, run IBU, verify local backup works
  - **Validation**: No errors during migration, both OADP and local backups coexist, OADP can be cleanly uninstalled after migration
  - **Owner**: QE team / Engineer A
  - **Deliverable**: Migration test case documented and passing

- **Task 3.3**: Failure injection testing
  - **Scenarios**:
    - Disk full during backup (fill `/var/lib/containers` to 95%, verify backup aborts with clear error)
    - API server crash mid-restore (kill `kube-apiserver` pod during wave 3, verify rollback triggered)
    - Corrupt backup file (modify YAML, verify checksum validation fails, restore aborted)
    - Network partition (disconnect node during restore, verify timeout and rollback)
  - **Validation**: All failures handled gracefully, no data loss, IBU CR status reflects error state
  - **Owner**: Engineer B
  - **Deliverable**: Failure test suite with 5+ scenarios, all passing

- **Task 3.4**: Performance benchmarking
  - **Metrics**:
    - Backup time: Measure for small (10 namespaces, 50 resources), medium (50 namespaces, 500 resources), large (100 namespaces, 5000 resources) workloads
    - Restore time: Same workload sizes
    - Disk space: Measure backup size (uncompressed YAML)
    - Compare to OADP baseline (S3 backup/restore times from existing IBU tests)
  - **Goal**: Local backup ≤ 50% of OADP time, restore ≤ 80% of OADP time (local I/O faster than S3 network)
  - **Owner**: Engineer A
  - **Deliverable**: Performance report with graphs, shared with team

---

### Phase 4: Documentation & Rollout (Week 7)

**Goal**: User-facing documentation, migration guide, release notes, prepare for GA

- **Task 4.1**: Update `docs/backuprestore-with-oadp.md`
  - **Change**: Rename to `docs/backup-and-restore.md`, document both OADP and local modes
  - **Sections**:
    - Overview of backup/restore in IBU
    - Local backup mode (default, recommended)
    - OADP mode (deprecated, for backward compatibility)
    - Comparison table (local vs OADP)
    - Migration procedure (OADP → local)
  - **Owner**: Tech writer / Engineer B
  - **Deliverable**: Updated documentation merged to `main` branch

- **Task 4.2**: Migration guide for existing users
  - **File**: `docs/migrate-oadp-to-local.md`
  - **Content**: Step-by-step procedure (see Section 5.4 above), troubleshooting tips, rollback instructions
  - **Audience**: Cluster admins currently using OADP
  - **Owner**: Tech writer / Engineer A
  - **Deliverable**: Migration guide reviewed and published

- **Task 4.3**: Release notes and deprecation announcement
  - **File**: `CHANGELOG.md` or release notes for LCA v1.X
  - **Content**:
    - **New Feature**: Built-in local backup/restore (no OADP required)
    - **Deprecation**: OADP mode deprecated, will be removed in v2.0 (6-8 weeks)
    - **Action Required**: Users should migrate to local mode (see migration guide)
  - **Owner**: Product manager / Engineer A
  - **Deliverable**: Release notes drafted, reviewed by team

- **Task 4.4**: CI pipeline updates
  - **Change**: Add local backup mode to CI test matrix (run IBU E2E tests with both `backupMode: local` and `backupMode: oadp`)
  - **Goal**: Ensure both modes are tested on every PR until OADP removed
  - **Owner**: Engineer B
  - **Deliverable**: CI config updated, test matrix includes both modes

---

## 7. Risk Analysis

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| **Disk space exhaustion during backup** | Medium | High (backup fails, upgrade aborted) | Pre-flight check for 10GB free space before backup; auto-cleanup of old backups (keep last 2); fail fast with clear error message if space insufficient |
| **Backup file corruption (bit rot, disk errors)** | Low | Critical (restore fails, data loss) | SHA256 checksum validation before restore; abort restore if checksum mismatch; keep multiple backup copies (last 3) for redundancy |
| **Restore failure mid-upgrade (API server crash, network partition)** | Medium | Critical (cluster in undefined state) | Transactional restore with rollback: track applied resources, delete on failure; dry-run validation before apply; E2E tests with failure injection |
| **Kubernetes version skew (backup from v1.28, restore to v1.29)** | Low | Medium (resource schema changes break restore) | Store Kubernetes version in backup metadata; validate API compatibility before restore; test upgrade paths (1.28→1.29, 1.29→1.30) |
| **Resource conflicts (restored resource already exists)** | Low | Medium (restore fails, user must manually resolve) | Dry-run detects conflicts before apply; log conflicting resources with names/namespaces; provide manual resolution guide (delete conflicting resources, retry) |
| **PV data loss (user assumes PV contents backed up)** | Low | Critical (user loses data, blames LCA) | Document clearly that PV data persists on `/var/lib/containers` (not backed up); add warning in IBU CR status: "PV data not backed up, ensure stateful workloads use /var/lib/containers partition" |
| **OADP migration breaks existing users** | Medium | High (users stuck on OADP, cannot upgrade LCA) | Dual mode support (OADP + local) for 2 releases; clear migration guide; automated migration test in CI; rollback procedure documented |
| **Performance regression (restore slower than OADP)** | Low | Medium (user complaints, delayed upgrades) | Benchmark during Phase 3; optimize wave parallelism if needed; document expected restore time (30-60s for typical SNO workload) |
| **Backup size exceeds expectations (>10GB)** | Low | Medium (disk space issues, slow I/O) | Add compression (gzip) if size >5GB; monitor backup size in CI tests; document max backup size (5GB uncompressed, 1GB compressed) |
| **Rollback mechanism fails (cannot delete applied resources)** | Low | Critical (cluster stuck in partial restore state) | Test rollback logic with failure injection; use finalizers to prevent accidental resource deletion; provide manual cleanup script |

**Highest Priority Risks (address first)**:
1. **Restore failure mid-upgrade**: Implement transactional rollback (Task 1.2) and test with failure injection (Task 3.3)
2. **Disk space exhaustion**: Pre-flight check (Task 1.3) and auto-cleanup (Task 1.1)
3. **OADP migration**: Dual mode support (Task 2.1-2.3) and migration testing (Task 3.2)

---

## 8. Success Criteria

**Must Have (Blocker for GA):**

- [ ] **Zero OADP dependency**: LCA can perform IBU backup/restore without OADP operator installed (validated by E2E test with `backupMode: local`, OADP not present)
- [ ] **Zero S3 dependency**: No external storage required, all backup data on local disk (validated by network-isolated test cluster)
- [ ] **E2E IBU test passing**: Full upgrade cycle (Prep → Upgrade → Rollback) with local backup/restore, sample workload restored successfully (validated in OpenShift CI)
- [ ] **Backward compatible upgrade**: Existing OADP users can upgrade LCA without breaking, migration path documented and tested (validated by Task 3.2)
- [ ] **Transactional restore**: Restore failures trigger automatic rollback, cluster returns to consistent state (validated by failure injection tests, Task 3.3)
- [ ] **Checksum validation**: Corrupt backups detected before restore, clear error message to user (validated by corrupt-file test, Task 3.3)

**Should Have (Important for UX):**

- [ ] **Restore performance**: Local restore completes within 20% of OADP baseline (measure in Task 3.4; OADP ~60s for 500 resources, local should be ≤72s)
- [ ] **Disk space efficiency**: Backup size <10GB for typical SNO workload (100 namespaces, 1000 resources); validated in CI tests
- [ ] **Automated cleanup**: Old backups purged automatically, user doesn't need to manually clean `/var/lib/containers` (Task 1.1)
- [ ] **Clear error messages**: Disk full, corrupt backup, restore conflict errors have actionable guidance (e.g., "Free 5GB on /var/lib/containers, retry backup")
- [ ] **Migration documentation**: Step-by-step guide for OADP → local migration, including troubleshooting (Task 4.2)

**Nice to Have (Future Enhancements):**

- [ ] **Backup compression**: Gzip YAML files to save disk space (5-10:1 ratio, reduces typical backup from 500MB to 50-100MB)
- [ ] **Progress metrics**: IBU CR status shows backup/restore progress (e.g., `status.backupProgress: "Exported 500/1000 resources"`)
- [ ] **Backup encryption at rest**: Encrypt backup directory with cluster key (protects sensitive data in ConfigMaps/Secrets)
- [ ] **Incremental backups**: Only backup changed resources since last backup (reduces disk I/O and space, but adds complexity)
- [ ] **Restore preview**: Dry-run restore with diff output (show what resources will be created/updated before applying)

**Acceptance Criteria for GA Release**:
- All "Must Have" criteria met and validated in CI
- At least 3 of 5 "Should Have" criteria met
- No P0/P1 bugs open related to local backup/restore
- Documentation complete (Tasks 4.1, 4.2, 4.3)
- Performance benchmarks meet targets (Task 3.4)

---

## 9. Open Questions for Review

1. **Backup retention policy**: Should we keep last 3 backups (current plan) or make it configurable via IBU CR field (`spec.backupRetentionCount: 3`)? Trade-off: Configurability vs simplicity. **Recommendation**: Start with hardcoded 3, add config field in v1.1 if users request it.

2. **Compression strategy**: Should we compress backups by default (gzip) to save disk space, or leave uncompressed for faster I/O and easier debugging? **Recommendation**: Start uncompressed (Phase 1-4), add optional compression in v1.1 if disk space becomes an issue. Measure backup sizes in Task 3.4 to decide.

3. **Restore parallelism**: Current plan uses sequential wave-based apply (Task 2.3). Should we parallelize within waves (e.g., apply all Deployments concurrently)? **Trade-off**: Faster restore vs complexity and race conditions. **Recommendation**: Start sequential, measure restore time in Task 3.4, add parallelism in v1.1 if restore >2 minutes for typical workload.

4. **OADP deprecation timeline**: Should we remove OADP code in v2.0 (6-8 weeks, 2 releases) or keep it longer (v3.0, 12-16 weeks, 4 releases)? **Recommendation**: 2 releases is sufficient for enterprise users to migrate; communicate deprecation clearly in release notes. If major feedback, extend to 3 releases.

5. **Backup encryption**: Is encryption at rest needed for initial release, or defer to v1.1? **Trade-off**: Security vs implementation complexity. **Recommendation**: Defer to v1.1; backups are on local disk (physical security), and PV data (which may contain sensitive data) is already unencrypted on `/var/lib/containers`. Document this limitation.

6. **Multi-stateroot scenario**: What happens if user has 3+ stateroots (e.g., original, upgraded, rolled-back)? Should we limit backup retention to avoid disk exhaustion? **Recommendation**: Cleanup logic (Task 1.1) purges old backups regardless of stateroot count; document that `/var/lib/containers` is shared, backups persist across stateroots.

7. **Integration with external backup tools**: Should we provide a hook for users to export backups to S3/NFS (e.g., post-backup script)? **Recommendation**: Not for initial release; users can manually copy `/var/lib/containers/lca-backups/` to external storage if needed. Add export hook in v1.1 if requested.

8. **~~Backup content specification API~~** (RESOLVED - see Decision 6): ~~How should users specify what to backup?~~ **Decision**: Reinterpret existing `spec.oadpContent` field for local mode (Option A). Users keep existing ConfigMaps with Velero Backup CRs; local implementation interprets the specs without creating OADP CRs. Future option: introduce `spec.backupContent` with LCA-native BackupSpec in v1.1+ if needed for local-specific features.

---

## 10. Next Steps (Immediate Actions)

1. **Approve this plan** (Week 0, Day 1-2):
   - Share plan with LCA team (engineers, QE, product manager)
   - Review meeting: discuss open questions (Section 9), finalize decisions
   - Sign-off from tech lead and product manager
   - **Owner**: Writer (share plan), Tech lead (approve)

2. **Create detailed design doc** (Week 0, Day 3-5):
   - Expand Section 5.2 into full API design (function signatures, structs, error types)
   - Sequence diagrams for backup/restore flows (visualize Section 5.3)
   - Code structure: package layout, file organization, import dependencies
   - **Owner**: Engineer A
   - **Deliverable**: Design doc in `docs/design/local-backup-restore.md`

3. **Prototype local backup** (Week 1, Day 1-3):
   - Quick PoC to validate disk I/O performance (write 1000 YAML files, measure time)
   - Test on SNO cluster with realistic workload (100 namespaces, 500 resources)
   - Goal: Confirm backup completes in <30s, disk I/O not a bottleneck
   - **Owner**: Engineer A
   - **Deliverable**: PoC code (throwaway), performance data shared with team

4. **Update IBU CR spec** (Week 1, Day 4-5):
   - Add `backupMode` field to `imagebasedupgrade_types.go` (Task 2.1)
   - Run `make generate manifests`, review CRD diff
   - Open PR with API change (for early feedback, merge after plan approval)
   - **Owner**: Engineer B
   - **Deliverable**: PR opened, CRD updated

5. **Start Phase 1 implementation** (Week 1, Day 5 onward):
   - Begin coding `internal/backuprestore/localbackup.go` (Task 1.1)
   - Set up unit test framework, write first test (export single namespace to YAML)
   - Goal: Validate approach before investing in full implementation
   - **Owner**: Engineer A
   - **Deliverable**: WIP PR with `localbackup.go` skeleton and first unit test

**Timeline Summary (Traditional Development)**:
- **Week 0**: Plan approval, design doc, PoC (5 days)
- **Weeks 1-2**: Phase 1 (foundation)
- **Weeks 3-4**: Phase 2 (integration)
- **Weeks 5-6**: Phase 3 (testing)
- **Week 7**: Phase 4 (documentation)
- **Total**: 7 weeks for full implementation + 2 weeks buffer = **9 weeks to GA**

**Realistic Timeline with AI-Assisted Coding** (e.g., Claude Code, GitHub Copilot):

| Phase | Traditional Estimate | AI-Assisted Estimate | Time Savings |
|-------|---------------------|---------------------|--------------|
| **Week 0**: Design doc, PoC | 5 days | **3 days** | -40% (AI drafts design, generates PoC) |
| **Phase 1**: Foundation (localbackup.go, localrestore.go) | 2 weeks | **4-5 days** | -60% (AI generates boilerplate, serialization logic, unit tests) |
| **Phase 2**: Integration (controller updates, API) | 2 weeks | **3-4 days** | -65% (AI updates handlers, generates CRD changes) |
| **Phase 3**: Testing (E2E, failure injection) | 2 weeks | **1 week** | -50% (AI generates test scaffolds, but validation still manual) |
| **Phase 4**: Documentation | 1 week | **2-3 days** | -60% (AI drafts docs from code) |
| **Total** | **9 weeks** | **3-4 weeks** | **-65%** |

**AI-Assisted Reality Check**: With AI code generation, Option 1 (custom backup) realistically takes **3-4 weeks**, not 9 weeks.

**Revised Comparison**:

| Metric | Option 1 (Custom) Traditional | **Option 1 (Custom) AI-Assisted** | Option 4B (Always-On MinIO) |
|--------|------------------------------|-----------------------------------|---------------------------|
| **Implementation time** | 9 weeks | **3-4 weeks** ✅ | 2-3 days |
| **Runtime overhead** | 0s | 0s | 0s |
| **Memory overhead** | 0 MB | 0 MB | 1 GB (0.4% of 256GB) |
| **OADP features** | No | No | Yes |
| **Risk** | High (new code) | High (new code) | Low (YAML only) |

**Updated Decision Guidance**:

**Choose Option 4B (Always-On MinIO) if:**
- **Fastest time to production** — 2-3 days vs 3-4 weeks
- **Need OADP features now** — Hooks, CSI snapshots
- **Lower risk tolerance** — Proven stack vs new code
- **Telco hardware** — 1 GB trivial on 256GB RAM

**Choose Option 1 (Custom, AI-Assisted) if:**
- **Zero dependency requirement** — Must eliminate OADP
- **3-4 week timeline acceptable** — vs 2-3 days for Option 4B
- **Long-term architectural purity** — Worth the extra 3 weeks
- **Simple backup needs** — No hooks/CSI required
- **Have AI coding tools available** — Claude Code, Copilot, etc.

**Team Size**: 2 engineers (Engineer A - backup/restore logic, Engineer B - API/integration/testing), 1 QE (E2E tests), 1 tech writer (docs)

**Dependencies**: None (this work removes the OADP dependency, doesn't introduce new ones)

**Risks to Timeline**:
- Failure injection testing (Task 3.3) may uncover edge cases requiring rework (+1-2 weeks)
- OADP migration (Task 3.2) may reveal backward compatibility issues (+1 week)
- Performance benchmarks (Task 3.4) may require optimization (+1 week)
- **Mitigation**: 2-week buffer, prioritize "Must Have" criteria, defer "Nice to Have" to v1.1 if needed
