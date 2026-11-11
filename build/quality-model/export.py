#!/usr/bin/env python3
"""Export TOPIQ-NR (KonIQ, ResNet-50) from pyiqa to a fixed-shape ONNX file.

Build-time tool only; nothing here ships in the choix binary. choix normally
downloads the pinned public export listed in internal/ai/local/models.go.
Use this script to reproduce that file, or to produce one to host yourself
if the upstream repository ever disappears (then update the URL and SHA-256
in models.go).

    uv venv --python 3.11 venv
    uv pip install --python venv/bin/python -r build/quality-model/requirements.txt
    venv/bin/python build/quality-model/export.py topiq_nr_koniq_384.onnx

Input  "image": float32 [1, 3, 384, 384], RGB scaled to 0..1 (ImageNet
normalisation happens inside the graph). Output "score": float32 [1, 1],
higher is better.

Licence: the weights are CC BY-NC-SA 4.0 and pyiqa is PolyForm Noncommercial
1.0.0. The exported file is an adaptation of the weights and carries the same
CC BY-NC-SA 4.0 terms (non-commercial, attribution, share-alike).
"""
import hashlib
import sys

import pyiqa
import torch

SIDE = 384


class Wrapper(torch.nn.Module):
    def __init__(self, net: torch.nn.Module) -> None:
        super().__init__()
        self.net = net

    def forward(self, image: torch.Tensor) -> torch.Tensor:
        return self.net(image).reshape(1, 1)


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__)
        return 2
    out = sys.argv[1]
    metric = pyiqa.create_metric("topiq_nr", device="cpu")  # KonIQ-10k checkpoint
    model = Wrapper(metric.net).eval()
    dummy = torch.rand(1, 3, SIDE, SIDE)
    torch.onnx.export(
        model,
        (dummy,),
        out,
        input_names=["image"],
        output_names=["score"],
        opset_version=18,
        dynamo=False,
        do_constant_folding=True,
    )
    with open(out, "rb") as f:
        digest = hashlib.sha256(f.read()).hexdigest()
    print(f"wrote {out}\nsha256 {digest}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
