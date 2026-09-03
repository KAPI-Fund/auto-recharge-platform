export async function runDryRun({ onProgress }) {
  const stages = [
    [15, "校验 Session"],
    [35, "准备充值订单"],
    [60, "执行支付任务"],
    [85, "等待上游确认"],
    [100, "充值完成"],
  ];
  for (const [progress, message] of stages) {
    await new Promise((resolve) => setTimeout(resolve, 220));
    await onProgress(progress, message);
  }
  return { status: "succeeded", message: "演示任务完成" };
}
