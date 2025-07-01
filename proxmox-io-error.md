# NFS Storage I/O Error Troubleshooting Guide

## Problem Summary
**Issue**: Proxmox VMs failing to create with NFS storage errors
**Error**: `Input/output error` when accessing NFS mounted storage
**Root Cause**: Filesystem corruption on NFS server's underlying storage

## Symptoms Observed
- VM creation fails with: `clone failed: mkdir /mnt/pve/nfs-iso/images/109: Input/output error`
- Cannot write to NFS mount: `touch: cannot touch '/mnt/pve/nfs-iso/test-write': Input/output error`
- NFS mount appears healthy in `df -h` but fails on actual I/O operations

## Diagnostic Steps

### 1. Initial Health Checks
```bash
# Check if NFS mount exists and shows space
df -h | grep nfs-iso

# Test basic write operation
touch /mnt/pve/nfs-iso/test-write
```

### 2. Network and NFS Service Verification
```bash
# Verify NFS server connectivity
ping <NFS_SERVER_IP>

# Check NFS exports are available
showmount -e <NFS_SERVER_IP>

# Test NFS port connectivity
telnet <NFS_SERVER_IP> 2049
```

### 3. Check Client-Side Logs
```bash
# Look for NFS-related errors
dmesg | tail -20
journalctl -u nfs-client.target -n 20

# Check NFS client services
systemctl status nfs-client.target
```

### 4. Server-Side Investigation
**On the NFS Server:**
```bash
# Test local filesystem access
ls -la /path/to/nfs/export

# Check filesystem health
df -h /path/to/nfs/export

# Verify NFS exports
exportfs -v

# Check NFS server status
systemctl status nfs-server
```

## Resolution Steps

### Step 1: Fix Server-Side Filesystem Corruption
```bash
# On NFS Server (192.168.90.103 in our case)

# Stop NFS services
sudo systemctl stop nfs-server

# Unmount the corrupted filesystem
sudo umount /mnt/nfs-storage

# Run filesystem check and repair
sudo fsck -f /dev/mapper/nfs--storage--vg-nfs--storage--lv

# If filesystem is in use, force unmount:
lsof +f -- /mnt/nfs-storage        # Find processes using it
umount -l /mnt/nfs-storage          # Lazy unmount if needed

# Remount the filesystem
sudo mount /mnt/nfs-storage

# Restart NFS services
sudo systemctl start nfs-server

# Verify local access works
ls -la /mnt/nfs-storage/nfs/proxmox-iso
```

### Step 2: Clear Stale Client Connections
```bash
# On NFS Client (Proxmox nodes)

# Unmount the stale NFS mount
sudo umount /mnt/pve/nfs-iso

# Remount with fresh connection
sudo mount -t nfs4 192.168.90.103:/mnt/nfs-storage/nfs/proxmox-iso /mnt/pve/nfs-iso

# Test write operation
touch /mnt/pve/nfs-iso/test-write
ls -la /mnt/pve/nfs-iso/test-write
rm /mnt/pve/nfs-iso/test-write
```

### Step 3: Alternative Mount Options (if needed)
```bash
# Try different NFS mount options for better reliability
sudo umount /mnt/pve/nfs-iso
sudo mount -t nfs4 -o rsize=8192,wsize=8192,hard,intr,timeo=14 \
    192.168.90.103:/mnt/nfs-storage/nfs/proxmox-iso /mnt/pve/nfs-iso
```

## Prevention Measures

### 1. Regular Filesystem Health Checks
```bash
# Schedule periodic filesystem checks
# Add to crontab on NFS server:
# 0 2 * * 0 /usr/bin/fsck -n /dev/mapper/nfs--storage--vg-nfs--storage--lv
```

### 2. Monitoring Setup
- Monitor NFS server disk health
- Set up alerts for filesystem errors in logs
- Monitor available space on NFS exports

### 3. Backup Strategy
- Regular backups of NFS server configuration
- Backup of critical data before filesystem maintenance
- Document NFS export configurations

## Workaround for Immediate Resolution
If NFS issues persist, temporarily use local storage:

```go
// In Pulumi/Terraform code:
DatastoreId: pulumi.String("local-lvm")  // Instead of "nfs-iso"
```

## Key Learnings
1. **I/O errors on mounted filesystems** often indicate underlying storage corruption
2. **NFS client cache** can hold stale connections even after server fixes
3. **Always check server-side first** before blaming network or client issues
4. **Fresh mounts** after server repairs are essential to clear stale handles

## Environment Details
- **NFS Server**: 192.168.90.103 (rajesh-pi)
- **NFS Export**: `/mnt/nfs-storage/nfs/proxmox-iso`
- **Client Mount**: `/mnt/pve/nfs-iso`
- **Underlying Storage**: LVM volume `/dev/mapper/nfs--storage--vg-nfs--storage--lv`
- **Filesystem**: ext4

---
**Date Fixed**: June 27, 2025  
**Fixed By**: Infrastructure troubleshooting session  
**Time to Resolution**: ~30 minutes
