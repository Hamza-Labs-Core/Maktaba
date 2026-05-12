import { Link } from "react-router-dom";

export function NotFound() {
  return (
    <section className="mkt-page mkt-notfound">
      <h1>404</h1>
      <p>This route doesn't exist.</p>
      <Link to="/library" className="mkt-btn mkt-btn--primary">
        Back to library
      </Link>
    </section>
  );
}
