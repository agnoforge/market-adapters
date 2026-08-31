// Package app holds the Composite Market Dataset use cases: the declaration
// ones — Create, Dataset, Datasets, View, Edit, Delete — and Build, which
// reconciles a declaration against the source data that actually exists.
//
// They orchestrate the composite domain over two ports: the Store, which
// persists declarations and what a Build produced, and the AcquisitionPort,
// which is the control plane this context asks Market Data Acquisition for.
package app
