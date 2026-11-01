// Package jobs is the persisted background job queue: leases, per-kind
// concurrency, cancellation, throttled progress and restart recovery.
//
// A job row is running if and only if it holds a lease. Claiming a job bumps
// attempts, which is also the fencing token: every write a running handler
// makes is conditioned on the row still being running with that attempts value,
// so a handler that lost its lease (cancelled, recovered, or too slow to renew)
// has its context cancelled with ErrLeaseLost and can never write a result.
// Start and a 30 s sweep requeue, hold or fail rows whose lease expired; Stop
// cancels handlers with ErrShutdown and requeues them without using an attempt.
package jobs
