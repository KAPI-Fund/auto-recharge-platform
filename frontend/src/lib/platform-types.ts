export type Plan = {
  id: string;
  code: string;
  name: string;
  description: string;
  providerPlanName: string;
  country: string;
  paymentRegion?: string;
  paymentCurrency?: string;
  currency: string;
  price: number;
  active: boolean;
  /** 0 means unlimited storefront sales. */
  saleLimit?: number;
  soldCount?: number;
  remainingQuantity?: number | null;
  soldOut?: boolean;
  purchaseEnabled?: boolean;
  availabilityLabel?: string;
};

export type StoreOrder = {
  id: string;
  orderNo: string;
  planCode: string;
  planName: string;
  status: "pending" | "paid" | "failed";
  amount: number;
  currency: string;
  cdkCode: string;
  phoneCountryCode: string;
  phoneNumber: string;
  email: string;
  createdAt: string;
  paidAt?: string | null;
};

export type StoreProduct = Plan & {
  published: boolean;
  deliveryMode: "generated_cdk";
};

export type Task = {
  id: string;
  traceId?: string;
  status: "queued" | "running" | "succeeded" | "failed" | "manual";
  progress: number;
  message: string;
  mode: "upstream" | "browser" | "dry_run" | "protocol";
  sessionPreview: string;
  upstreamOrderId: string;
  errorCode: string;
  errorMessage: string;
  createdAt: string;
  updatedAt: string;
  finishedAt?: string | null;
  cdk: { code: string; status: string };
  plan: { code: string; name: string; price: number; currency: string };
};

export type PublicTask = {
  id: string;
  status: "queued" | "running" | "succeeded" | "failed" | "manual";
  progress: number;
  message: string;
};

export type Overview = {
  cdkTotal: number;
  cdkAvailable: number;
  cdkUsed: number;
  taskTotal: number;
  taskRunning: number;
  taskSucceeded: number;
  cardActive: number;
};

export type CDK = {
  id: string;
  code: string;
  status: string;
  createdAt: string;
  usedAt?: string | null;
  plan: Plan;
};

export type Card = {
  id: string;
  last4: string;
  holder: string;
  status: string;
  active: boolean;
  usageCount: number;
  cooldownUntil?: string | null;
  createdAt: string;
};
