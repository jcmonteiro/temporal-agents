import type { ReactNode, Ref } from "react";
import { SafeMarkdown } from "./markdown";

export type SteeringDecision = "guide" | "skip" | "stop" | "continue" | "accept";
export type SteeringStep = "outcome" | "clarify" | "guidance" | "review";

export const DECISION_LABELS: Record<SteeringDecision, string> = {
  guide: "Build with guidance",
  skip: "Proceed without guidance",
  stop: "Stop the loop",
  continue: "Continue with a fresh pass budget",
  accept: "Accept the work as finished",
};

export const DECISION_CONSEQUENCES: Record<SteeringDecision, string> = {
  guide: "Implementation resumes with the guidance below.",
  skip: "Implementation resumes without operator guidance.",
  stop: "The review loop stops without another implementation pass.",
  continue: "The review loop receives a fresh pass budget.",
  accept: "The current work is accepted as finished.",
};

export const CONFIRM_LABELS: Record<SteeringDecision, string> = {
  guide: "Confirm and build",
  skip: "Confirm and proceed",
  stop: "Confirm stop",
  continue: "Confirm fresh pass budget",
  accept: "Confirm as finished",
};

export interface OutcomeOption {
  decision: SteeringDecision;
  description: string;
  danger?: boolean;
}

export const REVIEW_OUTCOMES: OutcomeOption[] = [
  {
    decision: "guide",
    description: "Clarify if needed, then prepare implementation direction.",
  },
  {
    decision: "skip",
    description: "Resume implementation without operator direction.",
  },
  {
    decision: "stop",
    description: "End the review without another implementation pass.",
    danger: true,
  },
];

export const PASS_LIMIT_OUTCOMES: OutcomeOption[] = [
  {
    decision: "continue",
    description: "Allow another bounded review pass.",
  },
  {
    decision: "accept",
    description: "Finish with the current implementation.",
  },
  {
    decision: "stop",
    description: "End the review without another pass.",
    danger: true,
  },
];

const STEP_LABELS: Record<SteeringStep, string> = {
  outcome: "Outcome",
  clarify: "Clarify",
  guidance: "Guidance",
  review: "Review",
};

export function stepsFor(
  choice: SteeringDecision | null,
  passLimit: boolean,
): SteeringStep[] {
  return passLimit || (choice !== null && choice !== "guide")
    ? ["outcome", "review"]
    : ["outcome", "clarify", "guidance", "review"];
}

export function SteeringProgress({
  current,
  steps,
}: {
  current: SteeringStep;
  steps: SteeringStep[];
}): ReactNode {
  const currentIndex = steps.indexOf(current);
  return (
    <nav className="steering-steps" aria-label="Steering progress">
      <ol>
        {steps.map((step, index) => (
          <li
            key={step}
            aria-current={step === current ? "step" : undefined}
            data-complete={index < currentIndex || undefined}
          >
            <span>{index + 1}</span>
            <strong>{STEP_LABELS[step]}</strong>
            {step === "clarify" && <small>Optional</small>}
          </li>
        ))}
      </ol>
    </nav>
  );
}

export function DecisionMaterialDisclosure({
  material,
}: {
  material?: string;
}): ReactNode {
  return (
    <details className="steering-material-disclosure">
      <summary>Review the decision material</summary>
      <SafeMarkdown
        className="steering-markdown steering-material-disclosure__content"
        tabIndex={0}
        text={material || "No review material was supplied for this round."}
      />
    </details>
  );
}

export function OutcomeChoices({
  firstRef,
  onChoose,
  options,
}: {
  firstRef: Ref<HTMLButtonElement>;
  onChoose(decision: SteeringDecision): void;
  options: OutcomeOption[];
}): ReactNode {
  return (
    <div className="steering-outcome__choices">
      {options.map((option, index) => (
        <button
          key={option.decision}
          ref={index === 0 ? firstRef : undefined}
          className={option.danger ? "steering-outcome__danger" : undefined}
          type="button"
          aria-label={DECISION_LABELS[option.decision]}
          onClick={() => onChoose(option.decision)}
        >
          <strong>{DECISION_LABELS[option.decision]}</strong>
          <span>{option.description}</span>
        </button>
      ))}
    </div>
  );
}
