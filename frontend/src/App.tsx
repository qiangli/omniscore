import { Route, Switch } from "wouter";
import { Home } from "./pages/Home";
import { Exam } from "./pages/Exam";
import { Review } from "./pages/Review";
import { Results } from "./pages/Results";

export default function App() {
  return (
    <Switch>
      <Route path="/" component={Home} />
      <Route path="/exam/:sessionId/review" component={Review} />
      <Route path="/exam/:sessionId" component={Exam} />
      <Route path="/results/:sessionId" component={Results} />
      <Route>
        <main className="p-8 text-ink/60">Not found.</main>
      </Route>
    </Switch>
  );
}
