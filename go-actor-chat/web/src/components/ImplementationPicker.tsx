import {
  IMPLEMENTATION_ORDER,
  IMPLEMENTATIONS,
  type ImplementationId,
} from "../lib/backends";

type Props = {
  onSelect: (id: ImplementationId) => void;
};

export default function ImplementationPicker({ onSelect }: Props) {
  return (
    <div className="prompt-screen">
      <div className="prompt-card impl-picker">
        <h1>Choose an implementation</h1>
        <p>
          Actor Chat runs the same protocol on different language runtimes. Pick
          which actor server to connect to for this session.
        </p>
        <div className="impl-options" role="list">
          {IMPLEMENTATION_ORDER.map((id) => {
            const impl = IMPLEMENTATIONS[id];
            return (
              <button
                key={id}
                type="button"
                role="listitem"
                className="impl-option"
                disabled={!impl.available}
                onClick={() => onSelect(id)}
              >
                <span className="impl-option-label">{impl.label}</span>
                <span className="impl-option-desc">{impl.description}</span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
