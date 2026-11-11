#!/usr/bin/env python3
"""Check a TOPIQ-NR ONNX file against the pyiqa PyTorch model.

    venv/bin/python build/quality-model/verify.py model.onnx photo1.jpg [photo2.jpg ...]

For every image it scores two native-resolution 384x384 crops with both
implementations and prints the absolute difference. Exits non-zero when the
largest difference exceeds 1e-4. Also prints the graph facts choix relies on
(single file, fixed input shape, standard operators only).
"""
import sys

import numpy as np
import onnx
import onnxruntime as ort
import pyiqa
import torch
from PIL import Image

SIDE = 384
TOLERANCE = 1e-4


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    path, images = sys.argv[1], sys.argv[2:]

    graph = onnx.load(path)
    print("opset:", [(o.domain or "ai.onnx", o.version) for o in graph.opset_import])
    for value in list(graph.graph.input) + list(graph.graph.output):
        dims = [d.dim_value for d in value.type.tensor_type.shape.dim]
        print("io:", value.name, dims)
    domains = sorted({n.domain or "ai.onnx" for n in graph.graph.node})
    external = any(t.data_location == 1 for t in graph.graph.initializer)
    print("node domains:", domains, "| external data:", external, "| functions:", len(graph.functions))

    session = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
    reference = pyiqa.create_metric("topiq_nr", device="cpu")
    worst = 0.0
    for name in images:
        image = Image.open(name).convert("RGB")
        width, height = image.size
        if width < SIDE or height < SIDE:
            print(f"{name}: smaller than {SIDE}px, skipped")
            continue
        for left, top in ((width - SIDE) // 2, (height - SIDE) // 2), (0, 0):
            crop = image.crop((left, top, left + SIDE, top + SIDE))
            x = (np.asarray(crop).astype(np.float32) / 255.0).transpose(2, 0, 1)[None]
            with torch.no_grad():
                want = float(reference(torch.from_numpy(x)).item())
            got = float(session.run(None, {"image": x})[0].reshape(-1)[0])
            worst = max(worst, abs(want - got))
            print(f"{name} @({left},{top}): torch={want:.6f} onnx={got:.6f} diff={abs(want - got):.2e}")
    print(f"max abs diff: {worst:.2e}")
    return 0 if worst <= TOLERANCE else 1


if __name__ == "__main__":
    raise SystemExit(main())
