# ai/local testdata

`identity.onnx` is a tiny model that returns its input unchanged. Generate with:

```python
import onnx
from onnx import helper, TensorProto

x = helper.make_tensor_value_info("x", TensorProto.FLOAT, [1, 4])
y = helper.make_tensor_value_info("y", TensorProto.FLOAT, [1, 4])
node = helper.make_node("Identity", ["x"], ["y"])
graph = helper.make_graph([node], "identity", [x], [y])
m = helper.make_model(graph, opset_imports=[helper.make_opsetid("", 17)])
onnx.save(m, "identity.onnx")
```

The file is *not* committed; tests that rely on it skip when absent.
