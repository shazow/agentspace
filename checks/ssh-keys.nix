{ pkgs }:

{
  consumer = {
    publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUQ2FsZrmb4kVgX9X6N1Llqfu6N7o8gBC4M0coYv0Ab agentspace-consumer-test";
    identityFile = "./id_ed25519";
  };

  graphical = {
    privateKey = pkgs.writeText "agentspace-graphical-test-key" ''
      -----BEGIN OPENSSH PRIVATE KEY-----
      b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
      QyNTUxOQAAACDs5JSkHdhirv4IlJL748zDDZ8ALUx+pK52d0sD1s2neQAAAKDFGLshxRi7
      IQAAAAtzc2gtZWQyNTUxOQAAACDs5JSkHdhirv4IlJL748zDDZ8ALUx+pK52d0sD1s2neQ
      AAAEBIFmjS+iJuRr/KCw7dOZpUHHWV8isoRjOO0dU2QQjQN+zklKQd2GKu/giUkvvjzMMN
      nwAtTH6krnZ3SwPWzad5AAAAGWFnZW50c3BhY2UtZ3JhcGhpY2FsLXRlc3QBAgME
      -----END OPENSSH PRIVATE KEY-----
    '';
    publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOzklKQd2GKu/giUkvvjzMMNnwAtTH6krnZ3SwPWzad5 agentspace-graphical-test";
    identityFile = "./id_ed25519";
  };

  passphraseProtected = {
    privateKey = pkgs.writeText "virtie-passphrase-test-key" ''
      -----BEGIN OPENSSH PRIVATE KEY-----
      b3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0AAAAGAAAABC6P5muIj
      Mitcjuj0yqzfEvAAAAGAAAAAEAAAAzAAAAC3NzaC1lZDI1NTE5AAAAIELNH7oZlQETmedW
      3DvYdyKEl4PJMo3MQECij+LtlPQFAAAAsLwMe02lLm69/c0loxzXskvyYVoggmV8cUdNFV
      VuOYy9JookOpg//cwY8/Jf7cHhumn9JiJ6mXJpF77a3qt8DkuNbmGGk5sk6xn6ANwM5koK
      v1Vi5NJ7CYNuifl0X08NZiCWcddMpkCvwbiMv9ZRHLLpNUlAqQzep9e7sakLRwvflIKChq
      eg34FS3urV8j0+zZ4WI3AukBEM80P2WVdc6+l7jBE8aQGEv4mLD+b6Q5IE
      -----END OPENSSH PRIVATE KEY-----
    '';
  };

  virtie = {
    publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBIqXkHFLTDd7n09425txXfdOgJDUb7CpMAdCPVRS94z agentspace-virtie-test";
    identityFile = ".agentspace-test/id_ed25519";
  };
}
