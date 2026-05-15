import { Route, Switch } from "wouter";
import { Home } from "./pages/Home";
import { Exam } from "./pages/Exam";
import { Review } from "./pages/Review";
import { Results } from "./pages/Results";
import { AdminLogin } from "./pages/AdminLogin";
import { AdminTests } from "./pages/AdminTests";
import { AdminReview } from "./pages/AdminReview";

export default function App() {
  return (
    <Switch>
      <Route path="/" component={Home} />
      <Route path="/exam/:sessionId/review" component={Review} />
      <Route path="/exam/:sessionId" component={Exam} />
      <Route path="/results/:sessionId" component={Results} />
      <Route path="/admin/login" component={AdminLogin} />
      <Route path="/admin/tests" component={AdminTests} />
      <Route path="/admin/review/:slug" component={AdminReview} />
      <Route>
        <main className="p-8 text-ink/60">Not found.</main>
      </Route>
    </Switch>
  );
}
