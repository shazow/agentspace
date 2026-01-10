clean: clean-podman-volumes

clean-podman-volumes:
	podman volume rm -a
