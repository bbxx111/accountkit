package user

// TestIsUniqueViolation exposes only the internal predicate to external tests.
var TestIsUniqueViolation = isUniqueViolation

// TestRepoQueries exposes fixture queries only while testing package user.
var TestRepoQueries = (*Repo).queries

// TestRepoWithTx exercises the real private transaction implementation.
var TestRepoWithTx = (*Repo).withTx

// TestRepoWithTxRaw exercises the real private transaction and its shared pgx.Tx.
var TestRepoWithTxRaw = (*Repo).withTxRaw
