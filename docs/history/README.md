# History

The architecture decision records that got falconet from an OpenTofu
repository's scripts to what is in this tree. They are kept for the incidents,
the measurements and the alternatives they weighed, and for nothing else.

**They do not describe the tree.** Several were superseded or amended in
place while the work ran, and a record read on its own will tell you things
that are no longer true — a Bun strangler, a bash `park` verb, a reference
review protocol, a config whose defaults name one repository's directories.
What is true now is in [`../decisions.md`](../decisions.md), which is the one
document to read before changing how falconet is built, and it is the only
one that is linted for agreement with the [charter](../charter.md).

New decisions are not written here. A decision is a row in the register and a
section beneath it, and the reasoning that does not fit there goes in the
commit or pull request that made the change. `git log` is the rest of the
record.
