# Tasks

- [x] 1 SHA-crypt (`$5$`, `$6$`) with the specification's test vectors
- [x] 2 `plaintext_password` write-only and optional, `encrypted_password` optional and computed, conflicting
- [x] 3 Plan modifier that checks the password against the hash; error when a new user has neither
- [x] 4 Create and Update read the password from the config
- [x] 5 State upgrade from version 0 drops the stored password
- [x] 6 Fake router stores real SHA-512 hashes
- [x] 7 Acceptance tests: import blocks with the password, the hash, neither and generated config plan nothing; a wrong password is sent
- [x] 8 Docs template with the import workflow, changelog
