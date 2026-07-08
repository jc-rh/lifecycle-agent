# OADP Replacement: Executive Summary for Product Management

**Date**: July 2026  
**Status**: Strategic Plan Complete, Pending Approval  
**Timeline**: 4-5 weeks implementation with AI-assisted development

---

## What We're Changing

**Current Architecture**:
- Image-based upgrades (IBU) require OADP operator + external S3 storage for backup/restore
- Dependencies: OADP operator (~500 MB), S3-compatible storage, network connectivity

**Proposed Architecture**:
- Built-in backup/restore using local node storage (`/var/lib/containers`)
- Zero external dependencies (no OADP, no S3)
- Native LCA implementation (~400 lines of code)

---

## Why This Matters

### Customer Benefits

| Benefit | Impact |
|---------|--------|
| **Simplified deployment** | Eliminates OADP operator installation (10 steps → 3 steps) |
| **Reduced memory footprint** | Removes OADP operator (~500 MB overhead) |
| **Air-gap compatible** | No external storage requirements, works fully disconnected |
| **Reduced attack surface** | Fewer dependencies = smaller security footprint |
| **Operational simplicity** | One less operator to maintain, upgrade, and troubleshoot |

### Telco SNO Fit
- Typical telco SNO: **256GB RAM**, local SSD/NVMe storage
- Backup storage: **<5GB** for typical workload (0.002% of disk)
- **Perfect fit** for single-node, air-gapped edge deployments

---

## Customer Impact

### ✅ What Stays the Same
- IBU workflow unchanged (Prep → Upgrade → Rollback)
- Zero downtime for applications
- Same rollback capabilities
- Wave-based restore ordering preserved

### ⚠️ What Changes

**API Change** (Breaking, but improved UX):

**Old API** (Velero-based, verbose):
```yaml
spec:
  oadpContent:
    - name: backup-config
---
# In ConfigMap: 50+ lines of Velero GVR specifications
includedClusterScopedResources:
  - group: rbac.authorization.k8s.io
    version: v1
    resource: clusterroles
  # ... repeat for 15+ resource types
```

**New API** (LCA-native, intuitive):
```yaml
spec:
  backupContent:
    - name: backup-config
---
# In ConfigMap: Simple application-centric
platform:
  includeACM: true     # One toggle vs 15 resource types
  includeLVMS: true
applications:
  - name: monitoring
    namespaces: [openshift-monitoring]
    resources: [configmaps, secrets]  # Simple strings
```

**Migration** (**Open for PM decision** — see Open Questions section):
- Option 1: Auto-conversion tool + dual-mode support (6-8 week transition)
- Option 2: Hard cutover with manual migration (document-only support)
- Option 3: Indefinite OADP support (maintain both implementations)

---

## ❓ Open Questions for Product Management

### Migration Strategy
**Question**: What is the acceptable migration path for existing OADP users?

**Options**:
1. **Auto-conversion tool + dual-mode support** (6-8 week transition window)
   - Tool converts OADP ConfigMaps to new format
   - Both APIs work during transition
   - Deprecation warnings guide users to migrate
   
2. **Hard cutover** (requires manual migration)
   - Document migration steps
   - Users must manually convert configs before upgrade
   - Faster development, more customer friction

3. **Indefinite OADP support** (maintain both implementations)
   - Keep OADP code path indefinitely
   - Highest maintenance burden, security risk (maintaining deprecated code)

**Impact**: Affects implementation timeline (Option 1: +1-2 weeks, Option 2: no impact, Option 3: ongoing)

### Backwards Compatibility
**Question**: Must we support rollback from new local backup to old OADP-based releases?

**Scenarios**:
- User upgrades LCA from v1.5 (OADP) → v2.0 (local backup)
- IBU rollback needs to restore to v1.5 cluster state
- Does the v1.5 LCA (OADP-based) need to read v2.0 local backups?

**Options**:
1. **No cross-version restore required** — users must complete IBU workflow on one version
2. **Support cross-version restore** — v1.5 can read v2.0 backups (requires backporting local backup code to v1.5)

**Impact**: Option 2 requires backporting local backup implementation to older releases (+2-3 weeks)

---

## Risks & Mitigations

| Risk | Impact | Mitigation | Status |
|------|--------|------------|--------|
| **Customer migration effort** | Medium | Depends on migration strategy (see Open Questions) | ❓ **PM Decision Required** |
| **Cross-version compatibility** | Medium | Depends on backwards compatibility requirements (see Open Questions) | ❓ **PM Decision Required** |
| **Feature parity concerns** | Low | Backup hooks not currently used by customers; can add if needed | ✅ Mitigated |
| **Implementation bugs** | Medium | Transactional restore with rollback, extensive testing | ✅ Mitigated |
| **Documentation gap** | Low | Clear migration guide, API examples, troubleshooting | ✅ Mitigated |

---

## Business Case

### Competitive Position
- **Simplifies** telco edge story: "No external dependencies required"
- **Reduces** operational overhead: One less operator to deploy, maintain, and upgrade
- **Aligns** with edge/air-gap positioning: Works fully disconnected

### Customer Segments
- **Telco SNO deployments** (primary): 256GB+ RAM, local SSD, air-gapped
- **Edge computing**: Resource-rich edge nodes
- **Not ideal for**: Highly resource-constrained edge (<16GB RAM, <10GB disk)

---

## Success Criteria

**Must Have (Blocker for GA)**:
- ✅ Zero OADP dependency
- ✅ Zero external storage dependency  
- ✅ E2E IBU test passing with local backup
- ✅ Feature parity with current OADP backup/restore
- ❓ Migration path tested and documented (**Open question for PM**)
- ❓ Backwards compatibility strategy (**Open question for PM**)

**Customer Validation**:
- Beta testing with 2-3 telco partners
- Migration testing on existing OADP deployments
- Performance benchmarking vs current OADP implementation

---

## Recommendation

**Approve Option 1 (Custom Local Backup) with LCA-Native API** for the following reasons:

1. **Strategic alignment**: Eliminates external dependencies, aligns with edge/air-gap positioning
2. **Customer value**: Simpler deployment, reduced operational overhead, smaller memory footprint
3. **API improvement**: Product manager approval obtained for cleaner, more intuitive API
4. **Realistic timeline**: 4-5 weeks base implementation + migration strategy time (based on PM decisions)
5. **Manageable migration**: Depends on migration strategy selected (see Open Questions)

**Alternative Considered & Rejected**:
- **Option 4B (Always-On MinIO)**: 2-3 days implementation, but keeps OADP dependency (~500 MB overhead), doesn't achieve strategic goal of zero external dependencies

---

## Next Steps (if Approved)

1. **Week 1**: API design review with engineering, finalize BackupSpec CRD
2. **Weeks 1-2**: Core implementation (localbackup.go, localrestore.go)
3. **Week 3**: Controller integration, migration tool
4. **Week 4-5**: E2E testing, failure injection, performance benchmarking
5. **Week 5**: Documentation, beta customer testing

**First Beta**: End of Week 5  
**GA Target**: 6-8 weeks (includes beta feedback cycle)

---

## Decision Required

### Primary Decision
**Approve strategic direction?**  
- [ ] **Yes, proceed with Option 1 (Custom Local Backup) + LCA-Native API**
- [ ] **Defer pending [specify concern]**
- [ ] **Prefer Option 4B (MinIO) instead** — keeps OADP dependency but 2-3 day implementation

### Required PM Decisions (if Option 1 approved)

**Migration Strategy** (see Open Questions section for details):
- [ ] **Option 1**: Auto-conversion tool + dual-mode support (6-8 weeks, +1-2 weeks dev time)
- [ ] **Option 2**: Hard cutover with manual migration (fastest, more customer friction)
- [ ] **Option 3**: Indefinite OADP support (highest maintenance burden)

**Backwards Compatibility**:
- [ ] **No cross-version restore required** — users must complete IBU workflow on one version
- [ ] **Support cross-version restore** — older releases can read new backups (+2-3 weeks backporting)

**Questions / Concerns**: _______________________________________________
