run:
	nix run

clean: clean-podman-volumes

clean-podman-volumes:
	podman volume rm -a

clean-worktrees:
	git worktree list --porcelain | grep "agent-" | cut -d' ' -f2- | xargs -I {} git worktree remove -f "{}"
