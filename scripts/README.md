Repo Airlock Workflow:

1. `agentspace-init` takes a directory of repos and creates local clones of them (using `git clone --reference` to share objects). The premise is that readonly repos are volume-mounted inside of a VM, and writeable versions are created by cloning.
2. `agentspace-bundle` takes a local repo and produces a static bundle (via `git bundle`) of all the changes since clone. The premise is that this bundle will be placed in a writeable volume-mounted "outbox" that will be processed by the host later.
3. `agentspace-import` takes a static bundle from the outbox, presents it for review, then merges it into the current repo.

This allows agents to operate within an isolated VM guest with a full writeable repo, possibly doing dangerous things in the process, but ultimately producing a useful byproduct. The byproduct needs to be separately reviewed by the host before merging and pushing.
