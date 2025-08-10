# raleigh

raleigh ~ rally ~ demo

Basic implementation of [DeMo](https://github.com/bloc97/DeMo) in Jax for TPUs. This repo is a launcher CLI in Go that manages the TPUs and launches the training process.

## Usage

```bash
# have gcloud and rsync installed
# a TRC quota helps
# set firewall rules to allow TCP traffic on all ports
git submodule update --init --recursive
go run launcher/*.go
```

## TODO

* Launcher
  * UI
    * Fix resizing
    * Add menu for checking on status of each TPU
  * TPU management
    * Use `gcloud` API to check on TPUs
  * Installation
    * Come up with a more consistent configuration method. Don't hardcode wandb netrc copying, etc.
  * Launching
    * Automatically take in running processes
      * Figure out a source of truth for TPU groups
    * Optimize port selection
      * Currently, we use arbitrary ports and need to open the firewall entirely
* Jax side
  * Separate out DeMo into a library
  * Staggered CPU tensor sending
  * P2P connection
  * Authentication
  * Test on v3-8, v2-8
* General
  * Save checkpoints, share them & take in new nodes at regular intervals
  * Keep persistent SSH connection or write a TPU-side client; have a clear data structure
  * Abstract away from TPUs and write tests
