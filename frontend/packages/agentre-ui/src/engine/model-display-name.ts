// modelDisplayName 模型在界面上的人读名：展示名优先，没填展示名回落模型 ID。
//
// 模型 ID 是发给上游的标识符，只在「整条就是标识符」的位置出现（模型表格副行、
// 编辑表单、品牌标识判定）；其余凡是向用户说出「哪个模型」的地方都走这里。
export function modelDisplayName(model: {
  name?: string;
  modelId: string;
}): string {
  return model.name || model.modelId;
}
