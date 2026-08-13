{
    description = "Development environment for Pixel Battle Molten";

    inputs = {
        nixpkgs.url = "github:nixos/nixpkgs/nixos-26.05";
        flake-utils.url = "github:numtide/flake-utils";
    };

    outputs = { self, nixpkgs, flake-utils }:
        flake-utils.lib.eachDefaultSystem (system:
            let
                pkgs = import nixpkgs {
                    inherit system;
                };
            in
            {
                packages.default = pkgs.buildGoModule {
                    pname = "molten";
                    version = "v0.1.1";

                    src = ./.;

                    vendorHash = "sha256-ytZ8w0Wn2ChtcTs8GKHmtHHdCgrNH3WIA7r4E87vFnU=";

                    env = {
                        CGO_ENABLED = 0;
                    };
                    ldflags = [ "-s" "-w" ];

                    nativeBuildInputs = [ pkgs.makeWrapper ];
                    postInstall = ''
                        wrapProgram $out/bin/molten \
                            --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg ]}
                    '';

                    meta = {
                        description = "High-performance, protobuf-based stream format for recording and broadcasting real-time pixel grids and collaborative canvases";
                        homepage = "https://github.com/pixelate-it/molten";
                        license = pkgs.lib.licenses.mpl20;
                        mainProgram = "molten";
                    };
                };

                devShells.default = pkgs.mkShell {
                    packages = with pkgs; [
                        go_1_26
                        gopls
                        ffmpeg
                        protobuf
                        protoc-gen-go
                    ];
                };
            }
        );
}
